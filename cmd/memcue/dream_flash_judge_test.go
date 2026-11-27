package main

import (
	"reflect"
	"testing"
)

func TestParseFlashNoiseLabels(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		keys    []string
		want    map[string]string
		wantErr bool
	}{
		{
			name: "grouped labels",
			raw:  `{"useful":["f01"],"noise":["f02"],"uncertain":[]}`,
			keys: []string{"f01", "f02"},
			want: map[string]string{"f01": "useful", "f02": "noise"},
		},
		{
			name: "flat labels remain compatible",
			raw:  `{"f01":"useful","f02":"noise"}`,
			keys: []string{"f01", "f02"},
			want: map[string]string{"f01": "useful", "f02": "noise"},
		},
		{
			name:    "unknown grouped ID",
			raw:     `{"useful":["f01","unknown"],"noise":["f02"],"uncertain":[]}`,
			keys:    []string{"f01", "f02"},
			wantErr: true,
		},
		{
			name:    "duplicate grouped ID",
			raw:     `{"useful":["f01"],"noise":["f01","f02"],"uncertain":[]}`,
			keys:    []string{"f01", "f02"},
			wantErr: true,
		},
		{
			name:    "missing grouped ID",
			raw:     `{"useful":["f01"],"noise":[],"uncertain":[]}`,
			keys:    []string{"f01", "f02"},
			wantErr: true,
		},
		{
			name:    "non-string grouped item",
			raw:     `{"useful":[1],"noise":["f02"],"uncertain":[]}`,
			keys:    []string{"f01", "f02"},
			wantErr: true,
		},
		{
			name:    "invalid grouped bucket value",
			raw:     `{"useful":"f01","noise":["f02"],"uncertain":[]}`,
			keys:    []string{"f01", "f02"},
			wantErr: true,
		},
		{
			name:    "invalid flat label type",
			raw:     `{"f01":1,"f02":"noise"}`,
			keys:    []string{"f01", "f02"},
			wantErr: true,
		},
		{
			name:    "duplicate grouped bucket key",
			raw:     `{"useful":["f01"],"useful":["f02"],"noise":[],"uncertain":[]}`,
			keys:    []string{"f01", "f02"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFlashNoiseLabels([]byte(tt.raw), tt.keys, "useful", "noise", "uncertain")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseFlashNoiseLabels() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFlashNoiseLabels() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseFlashNoiseLabels() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
