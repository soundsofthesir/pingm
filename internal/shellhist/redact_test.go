package shellhist

import "testing"

func TestRedact(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ping 10.0.0.1", "ping 10.0.0.1"},
		{"ssh -p 22 admin@router", "ssh -p 22 admin@router"},
		{"export GITHUB_TOKEN=abc123", "export GITHUB_TOKEN=***"},
		{"mysql --password hunter2 db", "mysql --password *** db"},
		{"curl -H 'Authorization: Bearer abc.def' https://x", "curl -H 'Authorization: Bearer *** https://x"},
		{"git clone https://me:s3cret@example.com/r.git", "git clone https://me:***@example.com/r.git"},
		{"echo sk-ant-api03-abcdefghijklmnopqrstu", "echo ***"},
		{`wifi-connect password="my pass"`, "wifi-connect password=***"},
	}
	for _, c := range cases {
		if got := Redact(c.in); got != c.want {
			t.Errorf("Redact(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
