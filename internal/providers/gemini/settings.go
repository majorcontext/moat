package gemini

// Settings represents Gemini CLI settings.json structure.
type Settings struct {
	Security SecuritySettings `json:"security"`
}

// SecuritySettings holds security-related settings.
type SecuritySettings struct {
	Auth AuthSettings `json:"auth"`
}

// AuthSettings holds authentication configuration.
type AuthSettings struct {
	SelectedType string `json:"selectedType"` // "oauth-personal", "gemini-api-key"
}

// OAuthCreds represents the ~/.gemini/oauth_creds.json file structure.
type OAuthCreds struct {
	AccessToken  string `json:"access_token"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
	IDToken      string `json:"id_token,omitempty"`
	ExpiryDate   int64  `json:"expiry_date"`
	RefreshToken string `json:"refresh_token"`
}
