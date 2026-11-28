package extract

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestContentOnlyExtractionContracts(t *testing.T) {
	for _, v := range []any{Fact{}} {
		typ := reflect.TypeOf(v)
		for _, name := range []string{"Keywords", "Queries", "Translation"} {
			if _, ok := typ.FieldByName(name); ok {
				t.Errorf("%s exposes %s", typ.Name(), name)
			}
		}
	}
	for name, raw := range map[string]string{"note": schema} {
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(err)
		}
		var visit func(any)
		visit = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				for k, v := range x {
					if k == "keywords" || k == "queries" || k == "expansions" || k == "translations" {
						t.Errorf("%s schema requests %s", name, k)
					}
					visit(v)
				}
			case []any:
				for _, v := range x {
					visit(v)
				}
			}
		}
		visit(value)
	}
}

func TestContentOnlyNoteEndpointContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct{ Role, Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if len(request.Messages) != 2 {
			t.Errorf("unexpected message count: %d", len(request.Messages))
		} else {
			for _, field := range []string{`"keywords"`, `"queries"`, `"translation"`} {
				if strings.Contains(request.Messages[0].Content, field) {
					t.Errorf("note endpoint still requests %s", field)
				}
			}
		}
		result := `{"facts":[{"content":"Cobalt observatory uses PROJ-1234."}],"dropped":""}`
		encoded, _ := json.Marshal(result)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":`+string(encoded)+`},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	useEndpoint(t, server.URL)
	result, err := Note(context.Background(), "isolated.md", "Cobalt observatory uses PROJ-1234.")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Facts) != 1 || result.Facts[0].Content != "Cobalt observatory uses PROJ-1234." {
		t.Errorf("extraction: %+v", result)
	}
}
