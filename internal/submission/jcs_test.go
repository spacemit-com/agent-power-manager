package submission

import (
	"testing"
)

func TestCanonicalizeJSONSortsKeys(t *testing.T) {
	input := "{\"z\":1,\"a\":\"b\",\"nested\":{\"y\":2,\"x\":\"v\"}}"
	got, err := CanonicalizeJSON([]byte(input))
	if err != nil {
		t.Fatalf("CanonicalizeJSON: %v", err)
	}
	want := "{\"a\":\"b\",\"nested\":{\"x\":\"v\",\"y\":2},\"z\":1}"
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestCanonicalizeJSONEscapesQuoteAndBackslash(t *testing.T) {
	input := "{\"s\":\"a\\\"b\\\\c\"}"
	got, err := CanonicalizeJSON([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"s\":\"a\\\"b\\\\c\"}"
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestCanonicalizeJSONEscapesControlChars(t *testing.T) {
	// 输入用非规范形式表达控制字符，期望规范化为短转义或小写 \uXXXX。
	input := "{\"s\":\"a\\u000ab\\u000Fc\\u0009d\"}"
	got, err := CanonicalizeJSON([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"s\":\"a\\nb\\u000fc\\td\"}"
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestCanonicalizeJSONNumbers(t *testing.T) {
	input := "{\"numbers\":[333333333.33333329,1E30,4.50,2e-3,0.000000000000000000000000001,7]}"
	got, err := CanonicalizeJSON([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"numbers\":[333333333.3333333,1e+30,4.5,0.002,1e-27,7]}"
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestCanonicalizeJSONSortsByUTF16CodeUnits(t *testing.T) {
	// U+1F600（代理对 D83D DE00）在 UTF-16 序中先于 U+FB33，
	// 而 UTF-8 字节序相反；须按 UTF-16 码元排序。
	input := "{\"\ufb33\":\"hebrew\",\"\U0001f600\":\"emoji\"}"
	got, err := CanonicalizeJSON([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"\U0001f600\":\"emoji\",\"\ufb33\":\"hebrew\"}"
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestCanonicalizeJSONRejectsTrailingValue(t *testing.T) {
	if _, err := CanonicalizeJSON([]byte("{} {}")); err == nil {
		t.Fatal("want error for trailing JSON value")
	}
}

func TestCanonicalizeJSONRejectsDuplicateKeys(t *testing.T) {
	if _, err := CanonicalizeJSON([]byte("{\"a\":1,\"a\":2}")); err == nil {
		t.Fatal("want error for duplicate object key")
	}
}
