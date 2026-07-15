package update

import "testing"

func TestParseVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    Version
		wantErr bool
	}{
		{in: "v7.0.0", want: Version{Major: 7, Minor: 0, Patch: 0, Raw: "v7.0.0"}},
		{in: "v4", want: Version{Major: 4, Minor: 0, Patch: 0, Raw: "v4"}},
		{in: "v1.2", want: Version{Major: 1, Minor: 2, Patch: 0, Raw: "v1.2"}},
		{in: "1.2.3", want: Version{Major: 1, Minor: 2, Patch: 3, Raw: "1.2.3"}},
		{in: "v2.0.0-rc.1", want: Version{Major: 2, Minor: 0, Patch: 0, Pre: "-rc.1", Raw: "v2.0.0-rc.1"}},
		{in: "latest", wantErr: true},
		{in: "main", wantErr: true},
		{in: "", wantErr: true},
		{in: "not-a-version", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := ParseVersion(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseVersion(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if got != tc.want {
				t.Errorf("ParseVersion(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestVersionCompare(t *testing.T) {
	t.Parallel()
	must := func(s string) Version {
		t.Helper()
		v, err := ParseVersion(s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if must("v7.1.0").Compare(must("v7.0.0")) <= 0 {
		t.Error("v7.1.0 should be > v7.0.0")
	}
	if must("v8.0.0").Compare(must("v7.9.9")) <= 0 {
		t.Error("v8.0.0 should be > v7.9.9")
	}
	if must("v1.0.0").Compare(must("v1.0.0-rc.1")) <= 0 {
		t.Error("stable should be > pre-release")
	}
	if !must("v7.1.0").SameMajor(must("v7.0.0")) {
		t.Error("same major")
	}
	if must("v7.1.0").SameMinor(must("v7.0.0")) {
		t.Error("different minor")
	}
}
