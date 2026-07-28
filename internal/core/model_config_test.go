package core

import "testing"

func TestParseDeepSeekReasonerAlias(t *testing.T) {
	got := ParseModelName("deepseek-reasoner")
	if got.Mode != "default" || !got.Thinking || got.Search {
		t.Fatalf("ParseModelName(deepseek-reasoner) = %#v", got)
	}
	if !IsSupportedModel("deepseek-reasoner") {
		t.Fatal("deepseek-reasoner should be a supported compatibility model")
	}
}
