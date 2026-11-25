package types

import "testing"

func TestValidateHTTPMaterialURL(t *testing.T) {
	good := []string{
		"http://203.0.113.7:50132/da/0c", "https://da.example/o", "https://da.example:443/o?x=1",
		"http://[2001:db8::1]:8700/da/0c", "http://da.example", "http://a:1/x", "http://a:65535/x",
	}
	bad := []string{
		"", "da.example/o", "file:///tmp/o", "ftp://da.example/o", "https:///o", "http://", "http:/da.example/o",
		"https://u:p@da.example/o", "https://u@da.example/o", "http://a:99999/x", "http://a:65536/x", "http://a:0/x",
		"http://a:080/x", "http://a:/x", "http://a:-1/x", "http://a:8o/x", " https://da.example/o", "https://da.example/o ",
		"https://da.example/o#frag", "https://da.example/o#", "https://da.example/a b", "https://da.example/a\tb",
		"https://da.example/a\nb", "https://da.example/\x00", "https://da.example/\x7f", "https://dá.example/o",
		"https://da.example\\@evil.example/o", "mailto:a@b", "HTTP://da.example/o",
	}
	for _, uri := range good {
		if !ValidateHTTPMaterialURL(uri, 512) {
			t.Errorf("refused %q", uri)
		}
	}
	for _, uri := range bad {
		if ValidateHTTPMaterialURL(uri, 512) {
			t.Errorf("accepted %q", uri)
		}
	}
	if ValidateHTTPMaterialURL("https://da.example/"+string(make([]byte, 600)), 512) {
		t.Error("accepted an oversized location")
	}
}

func TestValidateHTTPMaterialPrefix(t *testing.T) {
	for _, uri := range []string{"http://203.0.113.7:50132/da/", "https://da.example/", "https://da.example:8443/a/b/"} {
		if !ValidateHTTPMaterialPrefix(uri, 512) {
			t.Errorf("refused %q", uri)
		}
	}
	for _, uri := range []string{"https://da.example", "https://da.example/da", "https://da.example/da/?x=1", "https://da.example/da?x=/", "http://a:99999/", "file:///da/"} {
		if ValidateHTTPMaterialPrefix(uri, 512) {
			t.Errorf("accepted %q", uri)
		}
	}
}
