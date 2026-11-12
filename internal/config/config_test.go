package config

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestAPIURL(t *testing.T) {
	t.Setenv("CONNECT_API_URL", "")
	if APIURL("x") != "x" || APIURL("") != DefaultAPIURL {
		t.Fatal("flag/default resolution")
	}
	t.Setenv("CONNECT_API_URL", "y")
	if APIURL("") != "y" || APIURL("x") != "x" {
		t.Fatal("env resolution")
	}
}

func TestConfigDirHonoursXDG(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	t.Setenv("XDG_CONFIG_HOME", "/tmp/c")
	if d, _ := ConfigDir(); d != filepath.Join("/tmp/c", "connect") {
		t.Fatal(d)
	}
}

func TestCheckOriginNeedsHTTPSExceptLoopback(t *testing.T) {
	for u, ok := range map[string]bool{"https://api.example.com": true, "http://127.0.0.1:8080": true, "http://[::1]:1": true, "http://localhost:5000": true,
		"http://api.example.com": false, "http://127.0.0.1.evil.test": false, "ftp://localhost": false, "api.example.com": false} {
		if err := CheckOrigin(u); (err == nil) != ok {
			t.Error(u, err)
		}
	}
}
