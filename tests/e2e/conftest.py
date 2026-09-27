"""Pytest configuration and fixtures for onWatch E2E tests.

Session-scoped fixtures build and start the mock server and onwatch binary,
then tear them down after all tests complete.
"""
import os
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import Generator

import pytest
import urllib.request
import urllib.error

# Ports
ONWATCH_PORT = 19211
MOCK_PORT = 19212
BASE_URL = f"http://localhost:{ONWATCH_PORT}"
MOCK_URL = f"http://localhost:{MOCK_PORT}"

# Credentials
USERNAME = "admin"
PASSWORD = "testpass123"

# Paths (tempdir + .exe so the suite also runs on Windows)
PROJECT_ROOT = Path(__file__).resolve().parent.parent.parent
TMP_DIR = Path(tempfile.gettempdir())
EXE_SUFFIX = ".exe" if os.name == "nt" else ""
MOCK_BINARY = str(TMP_DIR / f"mockserver-test{EXE_SUFFIX}")
ONWATCH_BINARY = str(TMP_DIR / f"onwatch-test{EXE_SUFFIX}")
# E2E isolation: override HOME so the canonical DB path (~/.onwatch/data/onwatch.db)
# does not exist. This prevents main.go's fixExplicitDBPath() from redirecting to
# the production database.
E2E_HOME = str(TMP_DIR / "onwatch-e2e-home")
DB_PATH = str(TMP_DIR / "onwatch-e2e.db")


def pytest_configure(config) -> None:
    """Refuse to run on a developer Mac.

    onWatch auto-detects Cursor credentials from the macOS Keychain (not from
    HOME) and can refresh and rewrite them, so an e2e daemon on a signed-in
    Mac could rotate real tokens. CI runners are clean; set
    ONWATCH_E2E_ALLOW_HOST=1 only on a machine with no real credentials.
    """
    if sys.platform == "darwin" and not os.environ.get("CI") and os.environ.get("ONWATCH_E2E_ALLOW_HOST") != "1":
        pytest.exit(
            "e2e suite is CI-only on macOS: the daemon can read and refresh real "
            "Keychain credentials. Set ONWATCH_E2E_ALLOW_HOST=1 on a clean machine.",
            returncode=2,
        )


def _wait_for_http(url: str, timeout: float = 30.0, interval: float = 0.5) -> bool:
    """Poll an HTTP URL until it returns 200 or timeout is reached."""
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            req = urllib.request.Request(url, method="GET")
            resp = urllib.request.urlopen(req, timeout=5)
            if resp.status == 200:
                return True
        except (urllib.error.URLError, OSError, ConnectionRefusedError):
            pass
        time.sleep(interval)
    return False


def _kill_process(proc: subprocess.Popen) -> None:
    """Kill a subprocess and wait for it to exit."""
    if proc.poll() is None:
        try:
            proc.terminate()  # SIGTERM on Unix, TerminateProcess on Windows
            proc.wait(timeout=5)
        except (subprocess.TimeoutExpired, OSError):
            proc.kill()
            proc.wait(timeout=5)


def remove_instance_files(db_path: str, home: str) -> None:
    """Remove an onwatch instance's database files and HOME directory."""
    import shutil
    for path in [db_path, f"{db_path}-journal", f"{db_path}-wal", f"{db_path}-shm"]:
        try:
            os.unlink(path)
        except OSError:
            # Already gone, or still locked on Windows while the daemon exits;
            # start_onwatch clears leftovers before the next run.
            continue
    if os.path.exists(home):
        shutil.rmtree(home, ignore_errors=True)


def start_onwatch(port: int, db_path: str, home: str, provider_env: dict) -> subprocess.Popen:
    """Start the built onwatch binary in an isolated HOME and wait for /login.

    Anthropic is pinned to a fake token in statusline mode: onWatch never
    reads the real Claude Code keychain entry and never calls the usage or
    OAuth refresh endpoints, so running the suite on a developer machine
    cannot rotate (and log out) real credentials.
    """
    remove_instance_files(db_path, home)
    os.makedirs(home, exist_ok=True)
    env = os.environ.copy()
    env.update({
        "HOME": home,
        "USERPROFILE": home,  # Windows home directory
        # Windows keeps the test PID file under LOCALAPPDATA; a shared one
        # lets a second daemon stop the first on startup.
        "LOCALAPPDATA": os.path.join(home, "AppData", "Local"),
        "ONWATCH_ADMIN_PASS": PASSWORD,
        "ONWATCH_TEST_MODE": "1",
        "ANTHROPIC_TOKEN": "anth_test_e2e_token",
        "ANTHROPIC_SOURCE": "statusline",
    })
    env.update(provider_env)
    # A file, not a pipe: an unread pipe fills up and blocks the daemon. CI
    # prints these logs when a job fails. The child keeps its own handle, so
    # ours can be closed as soon as the process starts.
    with open(TMP_DIR / f"onwatch-e2e-{port}.log", "w") as log:
        proc = subprocess.Popen(
            [
                ONWATCH_BINARY,
                "--debug",
                f"--port={port}",
                "--interval=10",
                "--test",
                f"--db={db_path}",
            ],
            env=env,
            stdout=log,
            stderr=subprocess.STDOUT,
        )
    ready = _wait_for_http(f"http://localhost:{port}/login", timeout=30)
    if not ready:
        _kill_process(proc)
    assert ready, f"onWatch on port {port} did not start in time"
    return proc


@pytest.fixture(scope="session")
def mock_server() -> Generator[subprocess.Popen, None, None]:
    """Build and start the mock server binary."""
    # Build mock server
    result = subprocess.run(
        ["go", "build", "-o", MOCK_BINARY, "./internal/testutil/cmd/mockserver"],
        cwd=str(PROJECT_ROOT),
        capture_output=True,
        text=True,
        timeout=120,
    )
    assert result.returncode == 0, f"Mock server build failed: {result.stderr}"

    # Start mock server
    proc = subprocess.Popen(
        [
            MOCK_BINARY,
            f"--port={MOCK_PORT}",
            "--syn-key=syn_test_e2e_key",
            "--zai-key=zai_test_e2e_key",
            "--anth-token=anth_test_e2e_token",
        ],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )

    # Wait for mock server to be ready
    ready = _wait_for_http(f"{MOCK_URL}/admin/requests", timeout=15)
    assert ready, "Mock server did not start in time"

    yield proc

    _kill_process(proc)
    # Clean up binary
    try:
        os.unlink(MOCK_BINARY)
    except OSError:
        pass


@pytest.fixture(scope="session")
def onwatch_server(mock_server: subprocess.Popen) -> Generator[subprocess.Popen, None, None]:
    """Build and start the onwatch binary."""
    # Build onwatch
    build_cmd = ["go", "build"]
    build_tags = os.environ.get("ONWATCH_E2E_GO_BUILD_TAGS", "").strip()
    if build_tags:
        build_cmd.extend(["-tags", build_tags])
    build_cmd.extend(["-o", ONWATCH_BINARY, "./cmd/onwatch"])

    result = subprocess.run(
        build_cmd,
        cwd=str(PROJECT_ROOT),
        capture_output=True,
        text=True,
        timeout=120,
    )
    assert result.returncode == 0, f"onWatch build failed: {result.stderr}"

    proc = start_onwatch(ONWATCH_PORT, DB_PATH, E2E_HOME, {
        "SYNTHETIC_API_KEY": "syn_test_e2e_key",
        "ZAI_API_KEY": "zai_test_e2e_key",
        "ZAI_BASE_URL": f"http://localhost:{MOCK_PORT}",
    })

    yield proc

    _kill_process(proc)
    # Clean up
    try:
        os.unlink(ONWATCH_BINARY)
    except OSError:
        pass
    remove_instance_files(DB_PATH, E2E_HOME)


@pytest.fixture(autouse=True, scope="session")
def servers(mock_server: subprocess.Popen, onwatch_server: subprocess.Popen) -> Generator[None, None, None]:
    """Ensure both servers are running for all tests."""
    yield


@pytest.fixture
def authenticated_page(page):
    """Log in and return a page with a valid session cookie."""
    page.goto(f"{BASE_URL}/login")
    page.fill("#username", USERNAME)
    page.fill("#password", PASSWORD)
    page.click("button.login-button")
    # Wait for redirect to dashboard
    page.wait_for_url(f"{BASE_URL}/", timeout=10000)
    return page


@pytest.fixture
def dashboard_page(authenticated_page):
    """Return an authenticated page on the dashboard."""
    # Already on dashboard after login
    authenticated_page.wait_for_selector(".app-header", timeout=10000)
    return authenticated_page


@pytest.fixture
def settings_page(authenticated_page):
    """Navigate to the settings page and return the page."""
    authenticated_page.goto(f"{BASE_URL}/settings")
    authenticated_page.wait_for_selector(".settings-page", timeout=10000)
    return authenticated_page
