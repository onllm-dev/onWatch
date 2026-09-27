//go:build !windows

package main

import "testing"

func TestDaemonSysProcAttr_UnixSetsid(t *testing.T) {
	attr := daemonSysProcAttr()
	if attr == nil {
		t.Fatal("expected non-nil SysProcAttr")
	}
	if !attr.Setsid {
		t.Fatal("expected Setsid=true")
	}
}
