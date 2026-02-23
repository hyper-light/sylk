package oauth

import "testing"

func TestValidateGoogleAuth_AllowsMissingProjectID(t *testing.T) {
	auth := &GoogleOAuthAuth{
		AccessToken: "access_token",
		ProjectID:   "",
	}
	if err := validateGoogleAuth(auth); err != nil {
		t.Fatalf("expected missing project_id to be allowed, got %v", err)
	}
}

func TestValidateGoogleAuth_RequiresAccessToken(t *testing.T) {
	auth := &GoogleOAuthAuth{
		AccessToken: "",
	}
	if err := validateGoogleAuth(auth); err != ErrGoogleMissingToken {
		t.Fatalf("expected ErrGoogleMissingToken, got %v", err)
	}
}
