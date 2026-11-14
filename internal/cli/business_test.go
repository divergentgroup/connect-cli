package cli

import (
	"strings"
	"testing"
)

const profileBody = `{"id":"1","status":"draft","legalName":"Acme Ltd","legalForm":"sole_trader","country":"GB","contactPhone":"+447700900123","heldRequests":2,
"identityVerificationRequired":true,"registryLocked":false,"dashboardUrl":"https://dash.test/business",
"missing":[{"field":"addressLine","message":"Enter the first line of the address."},{"field":"useCase","message":"Say what the messages are for."}]}`

func TestBusinessShowPrintsMissingAndPointers(t *testing.T) {
	s := newStub(t, map[string]stubResp{"GET /v2/business-profile": {200, profileBody, nil}})
	_, do := testAppWithKey(t, s)
	code, out, errb := do("business", "show")
	if code != 0 {
		t.Fatal(code, errb)
	}
	for _, want := range []string{"draft", "Acme Ltd", "Missing addressLine: Enter the first line of the address.", "Missing useCase: Say what the messages are for.",
		"Held sender requests: 2", "Identity verification is required", "https://dash.test/business"} {
		if !strings.Contains(out, want) {
			t.Fatal(want, out)
		}
	}
	if code, out, _ = do("business", "show", "--json"); code != 0 || !strings.Contains(out, `"heldRequests": 2`) {
		t.Fatal(code, out)
	}
}

func TestBusinessSetSendsOnlyChangedFields(t *testing.T) {
	s := newStub(t, map[string]stubResp{"PATCH /v2/business-profile": {200, profileBody, nil}})
	_, do := testAppWithKey(t, s)
	code, _, errb := do("business", "set", "--legal-name", "Acme Ltd", "--legal-form", "SOLE_TRADER", "--contact-phone", "+447700900123", "--website", "")
	if code != 0 {
		t.Fatal(code, errb)
	}
	if got := string(s.body); got != `{"contactPhone":"+447700900123","legalForm":"sole_trader","legalName":"Acme Ltd","website":""}` &&
		got != `{"legalName":"Acme Ltd","legalForm":"sole_trader","contactPhone":"+447700900123","website":""}` {
		t.Fatal(got)
	}
	if strings.Contains(errb, "Warning") {
		t.Fatal(errb)
	}
}

func TestBusinessSetUsageErrors(t *testing.T) {
	s := newStub(t, map[string]stubResp{"PATCH /v2/business-profile": {200, profileBody, nil}})
	_, do := testAppWithKey(t, s)
	if code, _, errb := do("business", "set"); code != 2 || s.last != nil || !strings.Contains(errb, "--legal-name") {
		t.Fatal(code, errb)
	}
	if code, _, errb := do("business", "set", "--json"); code != 2 || s.last != nil {
		t.Fatal(code, errb)
	}
	if code, _, errb := do("business", "set", "--legal-form", "llc"); code != 2 || s.last != nil || !strings.Contains(errb, "limited_company") {
		t.Fatal(code, errb)
	}
	if code, _, errb := do("business", "set", "--use-case", "spam"); code != 2 || s.last != nil || !strings.Contains(errb, "marketing") {
		t.Fatal(code, errb)
	}
}

func TestBusinessSetWarnsAboutIgnoredFields(t *testing.T) {
	// A submitted profile keeps its name and city and takes the new contact email.
	body := `{"id":"1","status":"pending_review","legalName":"Acme Ltd","country":"GB","city":"Leeds","contactEmail":"new@acme.test","heldRequests":0,"missing":[]}`
	s := newStub(t, map[string]stubResp{"PATCH /v2/business-profile": {200, body, nil}})
	_, do := testAppWithKey(t, s)
	code, _, errb := do("business", "set", "--legal-name", "Other Ltd", "--city", "York", "--contact-email", "new@acme.test", "--country", "gb")
	if code != 0 || !strings.Contains(errb, "legal-name, city") || strings.Contains(errb, "contact-email") || strings.Contains(errb, "country") {
		t.Fatal(code, errb)
	}
}

func TestBusinessSubmitPrintsReviewNoticeAndFieldErrors(t *testing.T) {
	ok := `{"id":"1","status":"pending_review","country":"GB","heldRequests":1,"missing":[]}`
	s := newStub(t, map[string]stubResp{"POST /v2/business-profile/submit": {200, ok, nil}})
	_, do := testAppWithKey(t, s)
	code, out, errb := do("business", "submit")
	if code != 0 || !strings.Contains(out, "pending_review") || !strings.Contains(out, "held sender requests are released once it is verified") {
		t.Fatal(code, out, errb)
	}
	s.routes["POST /v2/business-profile/submit"] = stubResp{400, `{"type":"https://api.connect.ms/problems/validation_failed","title":"The request is not valid.","errors":{"legalForm":["Choose a legal form."],"contactPhone":["Enter a phone number."]}}`, nil}
	code, _, errb = do("business", "submit")
	if code != 4 || !strings.Contains(errb, "legalForm") || !strings.Contains(errb, "Choose a legal form.") || !strings.Contains(errb, "contactPhone") {
		t.Fatal(code, errb)
	}
}

func TestBusinessCompaniesAndCompany(t *testing.T) {
	list := `{"data":[{"number":"01234567","name":"ACME TRADING LIMITED","status":"active","address":"1 Anvil Way, London"}],"nextCursor":null}`
	s := newStub(t, map[string]stubResp{"GET /v2/business-profile/companies": {200, list, nil}, "POST /v2/business-profile/company": {200, profileBody, nil}})
	_, do := testAppWithKey(t, s)
	code, out, errb := do("business", "companies", "acme")
	if code != 0 || s.last.URL.Query().Get("q") != "acme" || !strings.Contains(out, "01234567\tACME TRADING LIMITED\tactive\t1 Anvil Way, London") {
		t.Fatal(code, out, errb)
	}
	if code, out, errb = do("business", "company", "01234567"); code != 0 || !strings.Contains(string(s.body), `{"number":"01234567"}`) || !strings.Contains(out, "Acme Ltd") {
		t.Fatal(code, out, errb, string(s.body))
	}
}
