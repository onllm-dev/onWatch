package api

import "testing"

func TestAlibabaCookieJar_HeaderForHost(t *testing.T) {
	jar := parseAlibabaCookieJar("# Netscape\n" +
		".alibabacloud.com\tTRUE\t/\tTRUE\t0\tcna\tabc\n" +
		"modelstudio.console.alibabacloud.com\tFALSE\t/\tTRUE\t0\thostonly\tx\n" +
		".mmstat.com\tTRUE\t/\tTRUE\t0\ttrack\t1\n")
	got := jar.headerFor("bailian-singapore-cs.alibabacloud.com")
	if got != "cna=abc" {
		t.Fatalf("zelda cookies = %q, want only shared .alibabacloud.com cookies", got)
	}
	got = jar.headerFor("modelstudio.console.alibabacloud.com")
	if got != "cna=abc; hostonly=x" {
		t.Fatalf("console cookies = %q", got)
	}
}
