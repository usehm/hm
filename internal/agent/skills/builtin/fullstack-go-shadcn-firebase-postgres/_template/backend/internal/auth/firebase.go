// Package auth verifies Firebase ID tokens sent by the admin UI.
package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	firebase "firebase.google.com/go/v4"
	fbauth "firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"
)

// User is who a verified token belongs to.
type User struct {
	UID   string
	Email string
}

// Verifier checks ID tokens. Verifying needs only the project ID: Firebase's
// public signing keys are fetched and cached.
type Verifier struct {
	client    *fbauth.Client
	devBypass bool
}

// NewVerifier builds a verifier for projectID. With devBypass, the token
// "dev" is accepted as a test user, for local development without Firebase.
func NewVerifier(ctx context.Context, projectID string, devBypass bool) (*Verifier, error) {
	v := &Verifier{devBypass: devBypass}
	if projectID == "" {
		return v, nil
	}
	// Verifying ID tokens needs no service account; without this the SDK
	// insists on Google application default credentials.
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: projectID}, option.WithoutAuthentication())
	if err != nil {
		return nil, err
	}
	if v.client, err = app.Auth(ctx); err != nil {
		return nil, err
	}
	return v, nil
}

// ErrNoToken means the request carried no bearer token.
var ErrNoToken = errors.New("missing bearer token")

// Verify checks the request's Authorization: Bearer <ID token>.
func (v *Verifier) Verify(r *http.Request) (User, error) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		return User{}, ErrNoToken
	}
	if v.devBypass && token == "dev" {
		return User{UID: "dev-user", Email: "dev@localhost"}, nil
	}
	if v.client == nil {
		return User{}, errors.New("firebase is not configured")
	}
	t, err := v.client.VerifyIDToken(r.Context(), token)
	if err != nil {
		return User{}, err
	}
	email, _ := t.Claims["email"].(string)
	return User{UID: t.UID, Email: email}, nil
}
