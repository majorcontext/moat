package pi

import (
	"strings"
	"testing"
)

func TestResolvePiProvider(t *testing.T) {
	all := piGrants{Anthropic: true, OpenAI: true, LunaRoute: true}
	tests := []struct {
		name         string
		provOverride string
		modelOver    string
		grants       piGrants
		wantProvider string
		wantModel    string
		wantErr      string // substring; "" = success
	}{
		{name: "infer anthropic", grants: piGrants{Anthropic: true}, wantProvider: "anthropic"},
		{name: "infer openai", grants: piGrants{OpenAI: true}, wantProvider: "openai"},
		{name: "infer lunaroute", grants: piGrants{LunaRoute: true}, wantProvider: "lunaroute"},
		{name: "model passthrough", grants: piGrants{Anthropic: true}, modelOver: "claude-opus-4-8", wantProvider: "anthropic", wantModel: "claude-opus-4-8"},
		{name: "anthropic+openai is ambiguous", grants: piGrants{Anthropic: true, OpenAI: true}, wantErr: "anthropic, openai"},
		{name: "anthropic+lunaroute is ambiguous", grants: piGrants{Anthropic: true, LunaRoute: true}, wantErr: "anthropic, lunaroute"},
		{name: "openai+lunaroute is ambiguous", grants: piGrants{OpenAI: true, LunaRoute: true}, wantErr: "openai, lunaroute"},
		{name: "all three is ambiguous", grants: all, wantErr: "anthropic, openai, lunaroute"},
		{name: "none names every grant", wantErr: "moat grant lunaroute"},
		{name: "override anthropic ok", provOverride: "anthropic", grants: piGrants{Anthropic: true}, wantProvider: "anthropic"},
		{name: "override openai ok", provOverride: "openai", grants: piGrants{OpenAI: true}, wantProvider: "openai"},
		{name: "override lunaroute ok", provOverride: "lunaroute", grants: piGrants{LunaRoute: true}, wantProvider: "lunaroute"},
		{name: "override anthropic but not granted", provOverride: "anthropic", grants: piGrants{OpenAI: true}, wantErr: "moat grant anthropic"},
		{name: "override openai but not granted", provOverride: "openai", grants: piGrants{Anthropic: true}, wantErr: "moat grant openai"},
		{name: "override lunaroute but not granted", provOverride: "lunaroute", grants: piGrants{Anthropic: true}, wantErr: "moat grant lunaroute"},
		{name: "override picks from all three", provOverride: "lunaroute", grants: all, wantProvider: "lunaroute"},
		{name: "unsupported backend fails hard", provOverride: "gemini", grants: piGrants{Anthropic: true}, wantErr: "anthropic, openai, lunaroute"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prov, model, err := resolvePiProvider(tt.provOverride, tt.modelOver, tt.grants)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got provider=%q", tt.wantErr, prov)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want it to contain %q", err, tt.wantErr)
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
