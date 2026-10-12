package larkcli

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
)

// The checked-in policy is reviewed against the official scope catalogue and
// consent page. See scopes.md for provenance and the update procedure.
//
//go:embed auto_approve_scopes.json
var autoApproveScopesJSON []byte

type authorizationScopes struct {
	Tenant []string `json:"tenant"`
	User   []string `json:"user"`
}

var autoApproveScopes = func() authorizationScopes {
	var scopes authorizationScopes
	if err := json.Unmarshal(autoApproveScopesJSON, &scopes); err != nil {
		panic(err)
	}
	return scopes
}()

func UserAuthorizationScopes() string {
	return strings.Join(autoApproveScopes.User, " ")
}

// CurrentUserAuthorizationScopes prevents recovery from resuming an older
// device authorization with a different permission selection.
func CurrentUserAuthorizationScopes(scope string) bool {
	values := strings.Fields(scope)
	slices.Sort(values)
	return slices.Equal(slices.Compact(values), autoApproveScopes.User)
}

// applicationAuthorizationURL uses the official SDK's addons wire format:
// JSON, gzip, then unpadded base64url. preset=false selects only the declared
// scopes instead of inheriting the platform's business permissions.
func applicationAuthorizationURL(raw string) (string, error) {
	if !AllowedAuthorizationURL(raw) {
		return "", errors.New("invalid application authorization URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(struct {
		Preset bool                `json:"preset"`
		Scopes authorizationScopes `json:"scopes"`
	}{Scopes: autoApproveScopes})
	if err != nil {
		return "", err
	}
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err = writer.Write(data); err != nil {
		return "", err
	}
	if err = writer.Close(); err != nil {
		return "", err
	}
	// The CLI landing page applies its own fixed template. The registration
	// service exposes /page/launcher for the SDK addons protocol.
	if u.Path == "/page/cli" {
		u.Path = "/page/launcher"
	}
	query := u.Query()
	query.Set("from", "sdk")
	query.Set("source", "csgclaw")
	query.Set("tp", "sdk")
	query.Set("addons", base64.RawURLEncoding.EncodeToString(buffer.Bytes()))
	u.RawQuery = query.Encode()
	return u.String(), nil
}
