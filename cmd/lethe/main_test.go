package main

import "testing"

func TestValidateBindAuth(t *testing.T) {
	cases := []struct {
		name          string
		addr          string
		apiKey        string
		allowInsecure bool
		wantErr       bool
	}{
		{"loopback without key", "127.0.0.1:18483", "", false, false},
		{"localhost without key", "localhost:18483", "", false, false},
		{"ipv6 loopback without key", "[::1]:18483", "", false, false},
		{"wildcard without key", ":18483", "", false, true},
		{"public without key", "0.0.0.0:18483", "", false, true},
		{"private ip without key", "192.168.1.10:18483", "", false, true},
		{"public with key", "0.0.0.0:18483", "lethe_secret", false, false},
		{"wildcard with key", ":18483", "lethe_secret", false, false},
		{"dev override", "0.0.0.0:18483", "", true, false},
		{"invalid address", "not-an-address", "", false, true},
	}
	for _, tc := range cases {
		err := validateBindAuth(tc.addr, tc.apiKey, tc.allowInsecure)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr = %v", tc.name, err, tc.wantErr)
		}
	}
}
