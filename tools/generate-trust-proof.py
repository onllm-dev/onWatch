#!/usr/bin/env python3
"""
Generate landing/trust-proof.json from live GitHub stargazer data.

Requirements:
  - gh CLI installed and authenticated (`gh auth status`)
  - Python dependencies: pycountry, geonamescache
"""

from __future__ import annotations

import argparse
import collections
import datetime as dt
import json
import os
import re
import subprocess
import sys
import tempfile
import unicodedata
from dataclasses import dataclass
from pathlib import Path
from typing import Dict, Iterable, List, Optional, Sequence, Tuple

geonamescache = None
pycountry = None


DEFAULT_OWNER = "onllm-dev"
DEFAULT_REPO = "onwatch"
DEFAULT_OUTPUT = "landing/trust-proof.json"

TRUST_ORG_TOTAL_MARKER = "__total_users_with_org_metadata"
COUNTRY_MAP_MIN_CITY_POP = 5000
ORG_EXAMPLE_LIMIT = 120

GRAPHQL_QUERY = """
query($owner: String!, $name: String!, $cursor: String) {
  repository(owner: $owner, name: $name) {
    stargazerCount
    stargazers(first: 100, after: $cursor) {
      pageInfo { hasNextPage endCursor }
      nodes {
        login
        ... on User {
          location
          company
        }
      }
    }
  }
}
"""


def ensure_runtime_dependencies() -> None:
    global geonamescache, pycountry  # pylint: disable=global-statement

    try:
        import geonamescache as _geonamescache  # type: ignore
        import pycountry as _pycountry  # type: ignore

        geonamescache = _geonamescache
        pycountry = _pycountry
        return
    except ImportError:
        pass

    boot_flag = "ONWATCH_TRUST_BOOTSTRAPPED"
    if os.environ.get(boot_flag) == "1":
        raise SystemExit(
            "Failed to bootstrap dependencies. Install `pycountry` and "
            "`geonamescache`, then rerun."
        )

    venv_dir = Path(tempfile.gettempdir()) / "onwatch-trust-proof-venv"
    venv_python = venv_dir / "bin" / "python"

    if not venv_python.exists():
        subprocess.run([sys.executable, "-m", "venv", str(venv_dir)], check=True)

    subprocess.run(
        [
            str(venv_python),
            "-m",
            "pip",
            "install",
            "--quiet",
            "--disable-pip-version-check",
            "pycountry",
            "geonamescache",
        ],
        check=True,
    )

    env = os.environ.copy()
    env[boot_flag] = "1"
    os.execve(str(venv_python), [str(venv_python), __file__, *sys.argv[1:]], env)


@dataclass
class StargazerProfile:
    login: str
    location: str
    company: str


def normalize_text(value: str) -> str:
    value = unicodedata.normalize("NFKD", value or "")
    value = "".join(ch for ch in value if not unicodedata.combining(ch))
    value = value.lower().strip()
    value = value.replace("&", " and ")
    value = re.sub(r"[^a-z0-9\s,./()|+-]", " ", value)
    value = re.sub(r"\s+", " ", value).strip()
    return value


def run_gh_json(args: Sequence[str]) -> dict:
    cmd = ["gh", *args]
    try:
        proc = subprocess.run(cmd, capture_output=True, text=True, check=True)
    except FileNotFoundError as exc:
        raise RuntimeError("gh CLI is required but was not found in PATH.") from exc
    except subprocess.CalledProcessError as exc:
        stderr = (exc.stderr or "").strip()
        raise RuntimeError(f"gh command failed: {' '.join(cmd)}\n{stderr}") from exc

    try:
        return json.loads(proc.stdout)
    except json.JSONDecodeError as exc:
        raise RuntimeError("Failed to parse JSON output from gh.") from exc


def ensure_gh_auth() -> None:
    try:
        subprocess.run(["gh", "auth", "status", "-h", "github.com"], check=True, capture_output=True, text=True)
    except subprocess.CalledProcessError as exc:
        raise RuntimeError(
            "GitHub auth is not ready. Run `gh auth login` first."
        ) from exc


def fetch_stargazers(owner: str, repo: str) -> Tuple[int, List[StargazerProfile]]:
    cursor: Optional[str] = None
    stargazer_count = 0
    profiles: List[StargazerProfile] = []

    while True:
        args = [
            "api",
            "graphql",
            "-f",
            f"query={GRAPHQL_QUERY}",
            "-F",
            f"owner={owner}",
            "-F",
            f"name={repo}",
        ]
        if cursor:
            args.extend(["-F", f"cursor={cursor}"])

        payload = run_gh_json(args)
        repo_data = (payload.get("data") or {}).get("repository") or {}
        if not repo_data:
            raise RuntimeError("Repository not found or GraphQL response was empty.")

        stargazer_count = int(repo_data.get("stargazerCount") or 0)
        stargazers = repo_data.get("stargazers") or {}
        nodes = stargazers.get("nodes") or []

        for node in nodes:
            if not isinstance(node, dict):
                continue
            login = (node.get("login") or "").strip()
            if not login:
                continue
            profiles.append(
                StargazerProfile(
                    login=login,
                    location=(node.get("location") or "").strip(),
                    company=(node.get("company") or "").strip(),
                )
            )

        page_info = stargazers.get("pageInfo") or {}
        if not page_info.get("hasNextPage"):
            break
        cursor = page_info.get("endCursor")
        if not cursor:
            break

    return stargazer_count, profiles


def pretty_country_name(name: str) -> str:
    remap = {
        "United States of America": "United States",
        "Russian Federation": "Russia",
        "Korea, Republic of": "South Korea",
        "Korea, Democratic People's Republic of": "North Korea",
        "Iran, Islamic Republic of": "Iran",
        "Taiwan, Province of China": "Taiwan",
        "Viet Nam": "Vietnam",
        "Moldova, Republic of": "Moldova",
        "Czech Republic": "Czechia",
        "Bolivia, Plurinational State of": "Bolivia",
        "Tanzania, United Republic of": "Tanzania",
        "Palestine, State of": "Palestine",
        "Lao People's Democratic Republic": "Laos",
        "Syrian Arab Republic": "Syria",
        "Brunei Darussalam": "Brunei",
        "Türkiye": "Turkey",
        "Holy See (Vatican City State)": "Vatican City",
    }
    return remap.get(name, name)


def build_country_aliases() -> Tuple[Dict[str, str], Dict[str, str]]:
    alias_to_country: Dict[str, str] = {}
    alpha2_to_country: Dict[str, str] = {}

    for country in pycountry.countries:
        canonical = pretty_country_name(country.name)
        alpha2_to_country[country.alpha_2] = canonical

        names = {country.name, country.alpha_2, country.alpha_3}
        if hasattr(country, "official_name"):
            names.add(country.official_name)
        if hasattr(country, "common_name"):
            names.add(country.common_name)

        for raw in names:
            key = normalize_text(raw)
            if key:
                alias_to_country[key] = canonical

    manual_aliases = {
        "usa": "United States",
        "u.s.a": "United States",
        "u.s": "United States",
        "us": "United States",
        "united states": "United States",
        "uk": "United Kingdom",
        "u.k": "United Kingdom",
        "great britain": "United Kingdom",
        "britain": "United Kingdom",
        "england": "United Kingdom",
        "scotland": "United Kingdom",
        "wales": "United Kingdom",
        "northern ireland": "United Kingdom",
        "uae": "United Arab Emirates",
        "u.a.e": "United Arab Emirates",
        "czech republic": "Czechia",
        "south korea": "South Korea",
        "north korea": "North Korea",
        "russia": "Russia",
        "vietnam": "Vietnam",
        "taiwan": "Taiwan",
        "hong kong sar": "Hong Kong",
        "hk": "Hong Kong",
    }
    for alias, country in manual_aliases.items():
        alias_to_country[normalize_text(alias)] = country

    return alias_to_country, alpha2_to_country


def build_subdivision_aliases(alpha2_to_country: Dict[str, str]) -> Dict[str, str]:
    out: Dict[str, str] = {}
    for subdivision in pycountry.subdivisions:
        code = getattr(subdivision, "code", "")
        country_code = code.split("-", 1)[0] if "-" in code else ""
        country = alpha2_to_country.get(country_code)
        if not country:
            continue

        name_key = normalize_text(getattr(subdivision, "name", ""))
        if name_key:
            out[name_key] = country

    us_state_abbrev = {
        "al", "ak", "az", "ar", "ca", "co", "ct", "de", "fl", "ga", "hi", "id",
        "il", "in", "ia", "ks", "ky", "la", "me", "md", "ma", "mi", "mn", "ms",
        "mo", "mt", "ne", "nv", "nh", "nj", "nm", "ny", "nc", "nd", "oh", "ok",
        "or", "pa", "ri", "sc", "sd", "tn", "tx", "ut", "vt", "va", "wa", "wv",
        "wi", "wy", "dc",
    }
    for code in us_state_abbrev:
        out[code] = "United States"

    return out


def build_city_country_map(alpha2_to_country: Dict[str, str]) -> Dict[str, str]:
    cache = geonamescache.GeonamesCache()
    cities = cache.get_cities()
    bucket: Dict[str, collections.Counter] = collections.defaultdict(collections.Counter)

    for city in cities.values():
        cc = city.get("countrycode")
        if cc not in alpha2_to_country:
            continue

        pop = int(city.get("population") or 0)
        if pop < COUNTRY_MAP_MIN_CITY_POP:
            continue

        country = alpha2_to_country[cc]
        names = {
            normalize_text(city.get("name", "")),
            normalize_text(city.get("ascii_name", "")),
        }
        weight = 2 if pop >= 100000 else 1
        for name in names:
            if name and len(name) >= 2:
                bucket[name][country] += weight

    city_map: Dict[str, str] = {}
    for city_name, counts in bucket.items():
        if not counts:
            continue
        if len(counts) == 1:
            city_map[city_name] = next(iter(counts.keys()))
            continue
        top_country, top_count = counts.most_common(1)[0]
        total = sum(counts.values())
        if top_count / total >= 0.75 and top_count >= 3:
            city_map[city_name] = top_country

    return city_map


def detect_country(
    location: str,
    alias_to_country: Dict[str, str],
    subdivision_aliases: Dict[str, str],
    city_country_map: Dict[str, str],
) -> Optional[str]:
    if not location:
        return None

    normalized = normalize_text(location)
    if not normalized:
        return None

    if normalized in {
        "remote", "worldwide", "global", "everywhere", "internet", "online", "earth", "planet earth"
    }:
        return None

    if normalized in alias_to_country:
        return alias_to_country[normalized]

    # Direct phrase match for country aliases.
    for alias in sorted(alias_to_country.keys(), key=len, reverse=True):
        if len(alias) < 3:
            continue
        if re.search(rf"\b{re.escape(alias)}\b", normalized):
            return alias_to_country[alias]

    tokens = [tok.strip() for tok in re.split(r"[,|;/()]+", normalized) if tok.strip()]

    # Last token is frequently country/subdivision.
    for tok in reversed(tokens):
        if tok in alias_to_country:
            return alias_to_country[tok]
        if tok in subdivision_aliases:
            return subdivision_aliases[tok]

    # Check city tokens and n-grams.
    for tok in tokens:
        if tok in city_country_map:
            return city_country_map[tok]

    words = normalized.split()
    for n in range(min(4, len(words)), 0, -1):
        for i in range(len(words) - n + 1):
            phrase = " ".join(words[i : i + n])
            if phrase in city_country_map:
                return city_country_map[phrase]

    return None


COMPANY_NOISE_PATTERNS = [
    r"^n/?a$",
    r"^none$",
    r"^na$",
    r"^self$",
    r"self\s*employed",
    r"freelance",
    r"independent",
    r"open\s*to\s*work",
    r"looking\s+for",
    r"student",
    r"retired",
]

COMPANY_ALIAS_PATTERNS: List[Tuple[str, str]] = [
    (r"\bmicrosoft\b", "Microsoft"),
    (r"\bgoogle\b|\balphabet\b", "Google"),
    (r"\bamazon\b|\baws\b|\bamzn\b", "Amazon"),
    (r"\bmeta\b|\bfacebook\b", "Meta"),
    (r"\bapple\b", "Apple"),
    (r"\bopenai\b", "OpenAI"),
    (r"\banthropic\b", "Anthropic"),
    (r"\bnvidia\b", "NVIDIA"),
    (r"\bintel\b", "Intel"),
    (r"\bibm\b", "IBM"),
    (r"\boracle\b", "Oracle"),
    (r"\bsalesforce\b", "Salesforce"),
    (r"\bred\s*hat\b", "Red Hat"),
    (r"\bshopify\b", "Shopify"),
    (r"\bcloudflare\b", "Cloudflare"),
    (r"\btailscale\b", "Tailscale"),
    (r"\bmeituan\b", "Meituan"),
    (r"\brackspace\b", "Rackspace Technology"),
    (r"\bsecureworks\b", "Secureworks"),
    (r"\bkhan\s+academy\b", "Khan Academy"),
    (r"\buniversal\s+music\b", "Universal Music Group"),
    (r"\bswisscom\b", "Swisscom"),
    (r"\bpetrobras\b", "Petrobras"),
    (r"\bonllm\b", "onllm.dev"),
]

MANUAL_FEATURED_ORGS = [
    "Microsoft",
    "onllm.dev",
    "Coriolis Technologies",
    "Tailscale",
    "Red Hat",
    "Salesforce",
    "Rackspace Technology",
    "Khan Academy",
    "Universal Music Group",
    "Swisscom",
    "Petrobras",
]

MAJOR_ORG_PRIORITY = [
    "Microsoft",
    "onllm.dev",
    "Coriolis Technologies",
    "Amazon",
    "Google",
    "Meta",
    "Apple",
    "OpenAI",
    "Anthropic",
    "NVIDIA",
    "Intel",
    "IBM",
    "Oracle",
    "Salesforce",
    "Red Hat",
    "Rackspace Technology",
    "Secureworks",
    "Cloudflare",
    "Tailscale",
    "Meituan",
    "Khan Academy",
    "Universal Music Group",
    "Swisscom",
    "Petrobras",
]

LEGAL_SUFFIX_RE = re.compile(
    r"\b(incorporated|inc|corp(?:oration)?|co(?:mpany)?|llc|ltda?|limited|gmbh|ag|bv|oy|ab|plc|pte\.?\s*ltd|s\.?a\.?|s\.?r\.?l\.?|pvt)\b\.?,?",
    re.IGNORECASE,
)

ORG_KEYWORDS = {
    "academy", "bank", "college", "consulting", "corp", "corporation", "group",
    "institute", "labs", "laboratories", "llc", "ltd", "maritime", "systems",
    "technology", "technologies", "university", "studio", "solutions", "team",
}

ORG_EXCLUDE_TERMS = {
    "contracted", "contractor", "developer", "freelance", "independent",
    "open to work", "private", "privately", "remote", "self employed",
    "student", "looking for", "hiring", "available for",
}

TITLECASE_LOWER_WORDS = {"and", "of", "the", "for", "to", "at", "in", "on", "by"}


def normalize_company(raw: str) -> Optional[str]:
    if not raw:
        return None

    value = raw.strip()
    if not value:
        return None

    value = re.sub(r"^[_@]+", "", value).strip()
    value = re.sub(r"[_@]+$", "", value).strip()
    value = re.sub(r"(?i)^(working\s+at|work\s+at|at)\s+", "", value)
    value = value.replace("｜", "|")
    if " @" in value:
        value = value.split(" @", 1)[0].strip()
    if value.startswith("@"):
        value = value.lstrip("@").strip()

    for sep in ("|", ";"):
        if sep in value:
            value = value.split(sep, 1)[0].strip()

    if "/" in value and "http" not in value.lower():
        parts = [part.strip() for part in value.split("/") if part.strip()]
        if parts:
            value = parts[0]

    if "(" in value and ")" in value:
        value = re.sub(r"\([^)]*\)", "", value).strip()

    value = value.strip(" .,-")
    if not value:
        return None

    normalized = normalize_text(value)
    if not normalized:
        return None

    # Filter noise entries (student, freelance, self-employed, etc.)
    for noise in COMPANY_NOISE_PATTERNS:
        if re.search(noise, normalized):
            return None

    for pattern, canonical in COMPANY_ALIAS_PATTERNS:
        if re.search(pattern, normalized):
            return canonical

    value = LEGAL_SUFFIX_RE.sub("", value)
    value = re.sub(r"\s+", " ", value).strip(" .,-")
    if len(value) < 2:
        return None

    # Keep mixed-case names as provided.
    if re.search(r"[a-z][A-Z]|[A-Z]{2,}", value):
        return value

    words = []
    for i, word in enumerate(value.split()):
        if i > 0 and word.lower() in TITLECASE_LOWER_WORDS:
            words.append(word.lower())
        else:
            words.append(word.capitalize())
    return " ".join(words)


def looks_like_handle_or_personal(name: str) -> bool:
    lowered = name.lower()
    if lowered.startswith("@"):
        return True
    if "http://" in lowered or "https://" in lowered:
        return True
    # Single lowercase token often indicates a personal handle.
    if " " not in name and re.fullmatch(r"[a-z0-9._-]{3,}", lowered):
        if "." in lowered:
            return True
        return True
    return False


def is_meaningful_org(name: str) -> bool:
    if not name or len(name) < 3:
        return False

    known_single = {
        "Microsoft", "Salesforce", "Meituan", "Tailscale", "Swisscom",
        "Petrobras", "onllm.dev",
    }
    if name in known_single:
        return True

    if looks_like_handle_or_personal(name):
        return False

    normalized = normalize_text(name)
    if any(term in normalized for term in ORG_EXCLUDE_TERMS):
        return False

    words = [w for w in re.split(r"\s+", normalized) if w]
    if any(word in ORG_KEYWORDS for word in words):
        return True

    # Multi-word proper names are acceptable as plain org text chips.
    if len(words) >= 2:
        return True

    # Preserve known single-word organizations (after alias canonicalization).
    return False


def select_org_examples(counter: collections.Counter) -> List[dict]:
    ranked = sorted(counter.items(), key=lambda kv: (-kv[1], kv[0].lower()))
    selected: List[Tuple[str, int]] = []
    selected_names = set()
    present = dict(ranked)

    def add_selected(name: str, count: int) -> None:
        if name in selected_names:
            return
        selected.append((name, count))
        selected_names.add(name)

    for name in MANUAL_FEATURED_ORGS:
        count = present.get(name)
        if count and is_meaningful_org(name):
            add_selected(name, count)

    for name in MAJOR_ORG_PRIORITY:
        count = present.get(name)
        if count and is_meaningful_org(name):
            add_selected(name, count)

    for name, count in ranked:
        if count >= 2 and is_meaningful_org(name):
            add_selected(name, count)

    for name, count in ranked:
        if is_meaningful_org(name):
            add_selected(name, count)

    return [{"name": name, "count": count} for name, count in selected[:ORG_EXAMPLE_LIMIT]]


def make_payload(owner: str, repo: str) -> Tuple[dict, dict]:
    stargazer_count, profiles = fetch_stargazers(owner, repo)
    if not profiles:
        raise RuntimeError("No stargazer profiles were returned from GitHub.")

    alias_to_country, alpha2_to_country = build_country_aliases()
    subdivision_aliases = build_subdivision_aliases(alpha2_to_country)
    city_country_map = build_city_country_map(alpha2_to_country)

    country_counter: collections.Counter = collections.Counter()
    org_counter: collections.Counter = collections.Counter()
    users_with_org_metadata = 0
    blank_location_count = 0
    untriangulated_nonblank_count = 0

    for profile in profiles:
        location = (profile.location or "").strip()
        if not location:
            blank_location_count += 1
        country = detect_country(
            location,
            alias_to_country=alias_to_country,
            subdivision_aliases=subdivision_aliases,
            city_country_map=city_country_map,
        )
        if not country and location:
            untriangulated_nonblank_count += 1
        if country:
            country_counter[country] += 1

        if profile.company:
            users_with_org_metadata += 1
            normalized_company = normalize_company(profile.company)
            if normalized_company:
                org_counter[normalized_company] += 1

    known_total = sum(country_counter.values())
    unknown_count = max(stargazer_count - known_total, 0)

    top_countries = [
        {"name": name, "count": count}
        for name, count in sorted(country_counter.items(), key=lambda kv: (-kv[1], kv[0]))
    ]
    top_countries.append({"name": "Unknown", "count": unknown_count})

    org_examples = [{"name": TRUST_ORG_TOTAL_MARKER, "count": users_with_org_metadata}]
    org_examples.extend(select_org_examples(org_counter))

    payload = {
        "as_of": dt.date.today().isoformat(),
        "stars_snapshot": stargazer_count,
        "countries_count": len(country_counter),
        "top_countries": top_countries,
        "org_examples": org_examples,
        "methodology": (
            "Aggregated from public GitHub stargazer profile metadata. "
            "Country spread resolves location text to countries; Unknown equals total stars minus known-country matches."
        ),
        "disclaimer": "Public profile metadata only. Organization names are examples and do not imply endorsement.",
    }

    stats = {
        "stars_snapshot": stargazer_count,
        "stargazer_profiles": len(profiles),
        "countries_count": len(country_counter),
        "known_country_total": known_total,
        "unknown_count": unknown_count,
        "users_with_org_metadata": users_with_org_metadata,
        "org_examples_written": len(org_examples) - 1,
        "blank_location_count": blank_location_count,
        "untriangulated_nonblank_count": untriangulated_nonblank_count,
    }
    return payload, stats


def parse_args(argv: Optional[Sequence[str]] = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Generate landing/trust-proof.json from GitHub stargazer metadata.")
    parser.add_argument("--owner", default=os.environ.get("TRUST_REPO_OWNER", DEFAULT_OWNER))
    parser.add_argument("--repo", default=os.environ.get("TRUST_REPO_NAME", DEFAULT_REPO))
    parser.add_argument("--output", default=DEFAULT_OUTPUT)
    return parser.parse_args(argv)


def main(argv: Optional[Sequence[str]] = None) -> int:
    ensure_runtime_dependencies()
    args = parse_args(argv)
    ensure_gh_auth()

    payload, stats = make_payload(args.owner, args.repo)
    out_path = Path(args.output)
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json.dumps(payload, indent=2, ensure_ascii=True) + "\n", encoding="utf-8")

    print(json.dumps(stats, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
