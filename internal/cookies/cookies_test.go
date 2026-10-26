package cookies

import (
	"testing"
	"time"
)

const tok = "Xq7TestTokenNotRealAbCdEf0123456"

func TestFormats(t *testing.T) {
	cases := map[string]string{
		"cookie-editor": `[{"domain":"booksy.com","expirationDate":1808595181.07,"name":"identity-es","value":"` + tok + `"},
			{"domain":"booksy.com","name":"_bf","value":"fp-1"},
			{"domain":"evil.example","name":"identity-us","value":"nope"}]`,
		"wrapper":  `{"url":"https://booksy.com/es-es/","cookies":[{"name":"identity-es","value":"` + tok + `","expirationDate":1808595181},{"name":"_bf","value":"fp-1"}]}`,
		"netscape": "# Netscape HTTP Cookie File\nbooksy.com\tFALSE\t/\tTRUE\t1808595181\tidentity-es\t" + tok + "\n.booksy.com\tTRUE\t/\tTRUE\t0\t_bf\tfp-1\nevil.example\tFALSE\t/\tTRUE\t0\tidentity-us\tnope\n",
		"header":   "Cookie: _dd_s_v2=x; identity-es=" + tok + "; _bf=fp-1",
	}
	for name, raw := range cases {
		cs, err := Parse(raw, Auto)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		ids := Identities(cs)
		if len(ids) != 1 {
			t.Fatalf("%s: want one identity, got %+v", name, ids)
		}
		id := ids[0]
		if id.Country != "es" || id.Token != tok || id.Fingerprint != "fp-1" {
			t.Errorf("%s: %+v", name, id)
		}
		if name != "header" && !id.Expires.Equal(time.Unix(1808595181, 0)) {
			t.Errorf("%s: expiry %v", name, id.Expires)
		}
	}
}

func TestBareToken(t *testing.T) {
	cs, err := Parse("  "+tok+"\n", Auto)
	if err != nil {
		t.Fatal(err)
	}
	ids := Identities(cs)
	if len(ids) != 1 || ids[0].Token != tok || ids[0].Country != "" {
		t.Fatalf("%+v", ids)
	}
	cs, _ = Parse("not a token!", Auto)
	if len(Identities(cs)) != 0 {
		t.Fatal("garbage should yield nothing")
	}
}

func TestMultipleCountries(t *testing.T) {
	cs, _ := Parse("identity-es="+tok+"; identity-pl=other", Header)
	if ids := Identities(cs); len(ids) != 2 || ids[1].Country != "pl" {
		t.Fatalf("%+v", ids)
	}
}
