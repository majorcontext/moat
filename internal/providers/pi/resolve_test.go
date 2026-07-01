package pi

import "testing"

func TestResolvePiProvider(t *testing.T) {
	tests := []struct {
		name         string
		provOverride string
		modelOver    string
		hasAnthropic bool
		hasOpenAI    bool
		wantProvider string
		wantModel    string
		wantErr      bool
	}{
		{name: "infer anthropic", hasAnthropic: true, wantProvider: "anthropic"},
		{name: "infer openai", hasOpenAI: true, wantProvider: "openai"},
		{name: "model passthrough", hasAnthropic: true, modelOver: "claude-opus-4-8", wantProvider: "anthropic", wantModel: "claude-opus-4-8"},
		{name: "both without override is ambiguous", hasAnthropic: true, hasOpenAI: true, wantErr: true},
		{name: "neither is an error", wantErr: true},
		{name: "override anthropic ok", provOverride: "anthropic", hasAnthropic: true, wantProvider: "anthropic"},
		{name: "override openai ok", provOverride: "openai", hasOpenAI: true, wantProvider: "openai"},
		{name: "override anthropic but not granted", provOverride: "anthropic", hasOpenAI: true, wantErr: true},
		{name: "override openai but not granted", provOverride: "openai", hasAnthropic: true, wantErr: true},
		{name: "override both present picks override", provOverride: "openai", hasAnthropic: true, hasOpenAI: true, wantProvider: "openai"},
		{name: "unsupported backend fails hard", provOverride: "gemini", hasAnthropic: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prov, model, err := resolvePiProvider(tt.provOverride, tt.modelOver, tt.hasAnthropic, tt.hasOpenAI)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got provider=%q", prov)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if prov != tt.wantProvider {
				t.Errorf("provider = %q, want %q", prov, tt.wantProvider)
			}
			if model != tt.wantModel {
				t.Errorf("model = %q, want %q", model, tt.wantModel)
			}
		})
	}
}
