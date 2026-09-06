package menubar

import "testing"

func TestLinuxSessionAvailable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		env   map[string]string
		files map[string]bool
		want  bool
	}{
		{"nothing", nil, nil, false},
		{"session bus env", map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus"}, nil, true},
		{"runtime dir bus socket", map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"}, map[string]bool{"/run/user/1000/bus": true}, true},
		{"runtime dir without bus", map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"}, nil, false},
		{"display only is not enough", map[string]string{"DISPLAY": ":0"}, nil, false},
		{"container marker wins", map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/x", "ONWATCH_DISABLE_TRAY": "1"}, nil, false},
		{"docker file wins", map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/x"}, map[string]bool{"/.dockerenv": true}, false},
	}
	for _, tc := range cases {
		getenv := func(k string) string { return tc.env[k] }
		exists := func(p string) bool { return tc.files[p] }
		if got := linuxSessionAvailable(getenv, exists); got != tc.want {
			t.Fatalf("%s: linuxSessionAvailable() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestWindowsSessionAvailable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"console session", map[string]string{"SESSIONNAME": "Console"}, true},
		{"rdp session", map[string]string{"SESSIONNAME": "RDP-Tcp#3"}, true},
		{"service session", nil, false},
		{"opt out", map[string]string{"SESSIONNAME": "Console", "ONWATCH_DISABLE_TRAY": "1"}, false},
	}
	for _, tc := range cases {
		getenv := func(k string) string { return tc.env[k] }
		if got := windowsSessionAvailable(getenv); got != tc.want {
			t.Fatalf("%s: windowsSessionAvailable() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSessionAvailableForGOOS(t *testing.T) {
	t.Parallel()
	getenv := func(k string) string {
		switch k {
		case "SESSIONNAME":
			return "Console"
		case "DBUS_SESSION_BUS_ADDRESS":
			return "unix:path=/run/user/1000/bus"
		}
		return ""
	}
	exists := func(string) bool { return false }
	if !sessionAvailableFor("darwin", getenv, exists) {
		t.Fatal("darwin should always report a session")
	}
	if !sessionAvailableFor("linux", getenv, exists) {
		t.Fatal("linux with session bus should report a session")
	}
	if !sessionAvailableFor("windows", getenv, exists) {
		t.Fatal("windows console session should report a session")
	}
	if sessionAvailableFor("freebsd", getenv, exists) {
		t.Fatal("unsupported platforms must not report a session")
	}
	optOut := func(k string) string {
		if k == "ONWATCH_DISABLE_TRAY" {
			return "true"
		}
		return getenv(k)
	}
	if sessionAvailableFor("darwin", optOut, exists) {
		t.Fatal("ONWATCH_DISABLE_TRAY must disable the companion on darwin too")
	}
}
