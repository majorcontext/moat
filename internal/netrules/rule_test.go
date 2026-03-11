package netrules

import "testing"

func TestParseRule(t *testing.T) {
	tests := []struct {
		input   string
		want    Rule
		wantErr bool
	}{
		{
			input: "allow GET /repos/*",
			want:  Rule{Action: "allow", Method: "GET", PathPattern: "/repos/*"},
		},
		{
			input: "deny DELETE /*",
			want:  Rule{Action: "deny", Method: "DELETE", PathPattern: "/*"},
		},
		{
			input: "allow * /user",
			want:  Rule{Action: "allow", Method: "*", PathPattern: "/user"},
		},
		{
			input: "deny * /admin/**",
			want:  Rule{Action: "deny", Method: "*", PathPattern: "/admin/**"},
		},
		{input: "block GET /foo", wantErr: true},
		{input: "allow /foo", wantErr: true},
		{input: "allow GET", wantErr: true},
		{input: "", wantErr: true},
		{input: "allow GET foo", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseRule(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
