package auth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func leadingZeroBits(b []byte) int {
	n := 0
	for _, x := range b {
		for i := 7; i >= 0; i-- {
			if x>>uint(i)&1 != 0 {
				return n
			}
			n++
		}
	}
	return n
}

func TestSolvePowVerifiesAtDifficulty12(t *testing.T) {
	sol, err := SolvePow(t.Context(), "challenge-abc", 12)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := strconv.ParseUint(sol, 10, 64); err != nil {
		t.Fatal(sol)
	}
	sum := sha256.Sum256([]byte("challenge-abc." + sol))
	if leadingZeroBits(sum[:]) < 12 {
		t.Fatal("not solved", sol)
	}
	if n, _ := strconv.Atoi(sol); n > 0 {
		sum := sha256.Sum256([]byte("challenge-abc." + strconv.Itoa(n-1)))
		if leadingZeroBits(sum[:]) >= 12 {
			t.Fatal("not the smallest")
		}
	}
}

func TestSolvePowRefusesHugeDifficultyAndHonoursCancel(t *testing.T) {
	if _, err := SolvePow(t.Context(), "c", 33); err == nil {
		t.Fatal("difficulty 33 accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := SolvePow(ctx, "c", 32); err == nil {
		t.Fatal("cancelled context ignored")
	}
}

func TestFinishCleansErrorDescription(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":"invalid_grant","error_description":"bad\u001b[31m red"}`)
	}))
	defer srv.Close()
	_, err := SignupFinish(t.Context(), srv.URL, &SignupState{}, "c")
	if err == nil || strings.Contains(err.Error(), "\x1b") {
		t.Fatal(err)
	}
}

func TestStartSolvesPowAndRepeatsRequest(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		bodies = append(bodies, m)
		if r.URL.Path != "/signup/agent/start" {
			t.Error(r.URL.Path)
		}
		if m["powSolution"] == nil {
			io.WriteString(w, `{"next":"pow","message":"work","powChallenge":"ch1","powDifficulty":8}`)
			return
		}
		io.WriteString(w, `{"signupId":"s1","next":"email_code","message":"Code sent"}`)
	}))
	defer srv.Close()
	st, step, err := SignupStart(t.Context(), srv.URL, StartRequest{Name: "A", Email: "a@x.io"})
	if err != nil || step.Next != "email_code" || st.ID != "s1" || st.Verifier == "" {
		t.Fatal(st, step, err)
	}
	if len(bodies) != 2 || bodies[1]["powToken"] != "ch1" || bodies[1]["email"] != "a@x.io" || bodies[1]["codeChallengeMethod"] != "S256" {
		t.Fatal(bodies)
	}
	sum := sha256.Sum256([]byte(st.Verifier))
	if bodies[1]["codeChallenge"] == "" || bodies[1]["codeChallenge"] != b64(sum[:]) || bodies[0]["codeChallenge"] != bodies[1]["codeChallenge"] {
		t.Fatal("challenge mismatch")
	}
}

func TestFinishSendsNoScopeAndBuildsProfile(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"at","refresh_token":"rt","expires_in":3600,"token_type":"Bearer","scope":"connect:sms.send"}`)
	}))
	defer srv.Close()
	p, err := SignupFinish(t.Context(), srv.URL, &SignupState{ID: "s1", Verifier: "v"}, "cred")
	if err != nil || p.AccessToken != "at" || p.RefreshToken != "rt" || len(p.Scopes) != 1 {
		t.Fatal(p, err)
	}
	if form.Get("grant_type") != "urn:divergent:params:oauth:grant-type:agent-signup" || form.Get("signup_credential") != "cred" ||
		form.Get("code_verifier") != "v" || form.Get("client_id") != "connect-cli" {
		t.Fatal(form)
	}
	if _, ok := form["scope"]; ok {
		t.Fatal("scope sent")
	}
}
