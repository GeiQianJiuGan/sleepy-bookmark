package api

import "testing"

// SetLocalLists lets the tests of the API serve lists from this machine.
func SetLocalLists(v bool) { localLists = v }

func TestFileURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/someone/lists/blob/main/dir/all.json":     "https://raw.githubusercontent.com/someone/lists/main/dir/all.json",
		"https://github.com/someone/lists/raw/main/all.json":          "https://raw.githubusercontent.com/someone/lists/main/all.json",
		"https://gist.github.com/someone/0123456789abcdef":            "https://gist.githubusercontent.com/someone/0123456789abcdef/raw",
		"https://raw.githubusercontent.com/someone/lists/main/a.json": "https://raw.githubusercontent.com/someone/lists/main/a.json",
		"https://example.invalid/blob/list.json":                      "https://example.invalid/blob/list.json",
	} {
		if got := fileURL(in); got != want {
			t.Errorf("%s: got %s", in, got)
		}
	}
}

func TestAListIsOnlyReadFromThePublicInternet(t *testing.T) {
	localLists = false
	defer func() { localLists = true }()
	for address, public := range map[string]bool{
		"127.0.0.1:8080":            false,
		"[::1]:80":                  false,
		"10.0.0.5:80":               false,
		"192.168.1.1:443":           false,
		"169.254.169.254:80":        false,
		"0.0.0.0:80":                false,
		"[fd00::1]:80":              false,
		"185.199.108.133:443":       true,
		"[2606:50c0:8000::154]:443": true,
	} {
		if err := publicOnly("tcp", address, nil); (err == nil) != public {
			t.Errorf("%s: public = %v, got %v", address, public, err)
		}
	}
}
