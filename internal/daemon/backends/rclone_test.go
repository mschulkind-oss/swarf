package backends

import "testing"

func TestMachineRemote(t *testing.T) {
	tests := []struct {
		remote, id, want string
	}{
		{"gdrive:swarf-store", "laptop", "gdrive:swarf-store/machines/laptop"},
		{"gdrive:swarf-store/", "laptop", "gdrive:swarf-store/machines/laptop"},
		{"local:/var/swarf", "a-b_c", "local:/var/swarf/machines/a-b_c"},
	}
	for _, tt := range tests {
		got := MachineRemote(tt.remote, tt.id)
		if got != tt.want {
			t.Errorf("MachineRemote(%q, %q) = %q, want %q", tt.remote, tt.id, got, tt.want)
		}
	}
}
