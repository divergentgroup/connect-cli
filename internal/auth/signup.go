package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/bits"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/divergentgroup/connect-cli/internal/config"
	"github.com/divergentgroup/connect-cli/internal/creds"
	"github.com/divergentgroup/connect-cli/internal/output"
	"golang.org/x/oauth2"
)

// maxPowDifficulty is a robustness cap so a broken server cannot make the client spin forever.
const maxPowDifficulty = 32

const signupGrant = "urn:divergent:params:oauth:grant-type:agent-signup"

// StartRequest is the body of POST /signup/agent/start; the code challenge and proof of work are filled in here.
type StartRequest struct {
	Name          string `json:"name"`
	Email         string `json:"email"`
	Phone         string `json:"phone"`
	WorkspaceName string `json:"workspaceName"`
	Brand         string `json:"brand,omitempty"`
	AgreeTerms    bool   `json:"agreeTerms"`
	AllowSetup    bool   `json:"allowSetup"`

	CodeChallenge       string `json:"codeChallenge"`
	CodeChallengeMethod string `json:"codeChallengeMethod"`
	PowToken            string `json:"powToken,omitempty"`
	PowSolution         string `json:"powSolution,omitempty"`
}

// SignupState is what survives between invocations: the sign-up id and the PKCE verifier.
type SignupState struct {
	ID       string `json:"id"`
	Verifier string `json:"verifier"`
}

// Step is the server's answer to every sign-up call, whatever the status.
type Step struct {
	SignupID      string `json:"signupId"`
	Next          string `json:"next"`
	Message       string `json:"message"`
	PowChallenge  string `json:"powChallenge"`
	PowDifficulty int    `json:"powDifficulty"`
	Credential    string `json:"credential"`
	Status        int    `json:"-"`
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// SolvePow finds the smallest decimal solution with sha256(challenge + "." + solution) having at least difficulty leading zero bits.
func SolvePow(ctx context.Context, challenge string, difficulty int) (string, error) {
	if difficulty > maxPowDifficulty {
		return "", fmt.Errorf("the sign-up service asked for proof of work of difficulty %d, which is more than this client will attempt", difficulty)
	}
	buf := []byte(challenge + ".")
	for n := uint64(0); ; n++ {
		if n%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		sum := sha256.Sum256(strconv.AppendUint(buf[:len(buf):len(buf)], n, 10))
		z := 0
		for _, b := range sum {
			if b == 0 {
				z += 8
				continue
			}
			z += bits.LeadingZeros8(b)
			break
		}
		if z >= difficulty {
			return strconv.FormatUint(n, 10), nil
		}
	}
}

// The sign-up calls; the final code provisions the account and workspace, which can take a while.
var signupHTTP = &http.Client{Timeout: 90 * time.Second}

func post(ctx context.Context, url string, body any) (*Step, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := signupHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	s := &Step{Status: resp.StatusCode}
	if err := json.Unmarshal(raw, s); err != nil || s.Next == "" {
		return nil, fmt.Errorf("unexpected response from the sign-up service (HTTP %d)", resp.StatusCode)
	}
	return s, nil
}

// powRetry calls send, and while the server asks for proof of work solves it and repeats the same request.
func powRetry(ctx context.Context, send func(token, solution string) (*Step, error)) (*Step, error) {
	step, err := send("", "")
	for i := 0; err == nil && step.Next == "pow" && i < 3; i++ {
		var sol string
		if sol, err = SolvePow(ctx, step.PowChallenge, step.PowDifficulty); err != nil {
			return nil, err
		}
		step, err = send(step.PowChallenge, sol)
	}
	return step, err
}

// SignupStart begins a sign-up. The returned state has an empty ID unless the server created a sign-up.
func SignupStart(ctx context.Context, issuer string, req StartRequest) (*SignupState, *Step, error) {
	v := make([]byte, 32)
	if _, err := rand.Read(v); err != nil {
		return nil, nil, err
	}
	st := &SignupState{Verifier: b64(v)}
	sum := sha256.Sum256([]byte(st.Verifier))
	req.CodeChallenge, req.CodeChallengeMethod = b64(sum[:]), "S256"
	step, err := powRetry(ctx, func(token, solution string) (*Step, error) {
		req.PowToken, req.PowSolution = token, solution
		return post(ctx, issuer+"/signup/agent/start", req)
	})
	if err == nil && step.Status == http.StatusOK {
		st.ID = step.SignupID
	}
	return st, step, err
}

// SignupCode submits an email, phone or authenticator code.
func SignupCode(ctx context.Context, issuer string, st *SignupState, code string) (*Step, error) {
	return post(ctx, issuer+"/signup/agent/"+url.PathEscape(st.ID)+"/code", map[string]string{"code": code})
}

// SignupResend asks for the current code to be sent again.
func SignupResend(ctx context.Context, issuer string, st *SignupState) (*Step, error) {
	return powRetry(ctx, func(token, solution string) (*Step, error) {
		body := map[string]string{}
		if token != "" {
			body["powToken"], body["powSolution"] = token, solution
		}
		return post(ctx, issuer+"/signup/agent/"+url.PathEscape(st.ID)+"/resend", body)
	})
}

// SignupFinish redeems the credential from a "done" step for a normal token profile.
func SignupFinish(ctx context.Context, issuer string, st *SignupState, credential string) (*creds.Profile, error) {
	form := url.Values{"grant_type": {signupGrant}, "signup_credential": {credential}, "code_verifier": {st.Verifier}, "client_id": {config.ClientID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuer+"/connect/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := signupHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var t struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
		Description  string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&t); err != nil || resp.StatusCode != http.StatusOK || t.AccessToken == "" {
		return nil, fmt.Errorf("could not redeem the sign-up (HTTP %d) %s", resp.StatusCode, output.Clean(t.Description))
	}
	tok := (&oauth2.Token{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, Expiry: time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)}).
		WithExtra(map[string]any{"scope": t.Scope})
	return ProfileFromToken(tok), nil
}
