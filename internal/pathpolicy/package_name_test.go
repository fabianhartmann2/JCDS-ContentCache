package pathpolicy

import "testing"

func TestValidPackageName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		valid bool
	}{
		{name: "ExampleFile.pkg", valid: true},
		{name: "Microsoft Office (16.99)+arm64.pkg", valid: true},
		{name: "example.PKG", valid: false},
		{name: "../secret.pkg", valid: false},
		{name: "folder/example.pkg", valid: false},
		{name: `folder\\example.pkg`, valid: false},
		{name: "https:example.pkg", valid: false},
		{name: ".hidden.pkg", valid: false},
		{name: "example.pkg?url=https://invalid", valid: false},
		{name: "example.dmg", valid: false},
		{name: "", valid: false},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := ValidPackageName(test.name); got != test.valid {
				t.Fatalf("ValidPackageName(%q) = %v, want %v", test.name, got, test.valid)
			}
		})
	}
}

func TestPackageName(t *testing.T) {
	name, err := PackageName("/packages/ExampleFile.pkg")
	if err != nil {
		t.Fatalf("PackageName() error = %v", err)
	}
	if name != "ExampleFile.pkg" {
		t.Fatalf("PackageName() = %q", name)
	}
}
