package doctor

import "testing"

func TestExtractExecStartPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "modern systemctl show",
			in:   "{ path=/home/me/.local/bin/swarf ; argv[]=/home/me/.local/bin/swarf daemon start --foreground ; ignore_errors=no ; start_time=[n/a] }",
			want: "/home/me/.local/bin/swarf",
		},
		{
			name: "no path= prefix, falls back to first absolute token",
			in:   "/home/me/.local/bin/swarf daemon start --foreground",
			want: "/home/me/.local/bin/swarf",
		},
		{
			name: "empty",
			in:   "",
			want: "",
		},
		{
			name: "no absolute path at all",
			in:   "swarf daemon start",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractExecStartPath(tc.in)
			if got != tc.want {
				t.Fatalf("extractExecStartPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
