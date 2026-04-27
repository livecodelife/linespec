package verify

import "testing"

func TestCompareJSON(t *testing.T) {
	tests := []struct {
		name     string
		expected interface{}
		actual   interface{}
		wantErr  bool
	}{
		{
			name:     "identical flat objects",
			expected: map[string]interface{}{"a": "1", "b": float64(2)},
			actual:   map[string]interface{}{"a": "1", "b": float64(2)},
			wantErr:  false,
		},
		{
			name:     "extra keys in actual are ignored",
			expected: map[string]interface{}{"a": "1"},
			actual:   map[string]interface{}{"a": "1", "b": "extra"},
			wantErr:  false,
		},
		{
			name:     "missing key in actual",
			expected: map[string]interface{}{"a": "1", "b": "2"},
			actual:   map[string]interface{}{"a": "1"},
			wantErr:  true,
		},
		{
			name:     "scalar value mismatch",
			expected: map[string]interface{}{"a": "1"},
			actual:   map[string]interface{}{"a": "2"},
			wantErr:  true,
		},
		{
			name:     "nested object match",
			expected: map[string]interface{}{"user": map[string]interface{}{"name": "Alice"}},
			actual:   map[string]interface{}{"user": map[string]interface{}{"name": "Alice", "age": float64(30)}},
			wantErr:  false,
		},
		{
			name:     "nested object mismatch",
			expected: map[string]interface{}{"user": map[string]interface{}{"name": "Alice"}},
			actual:   map[string]interface{}{"user": map[string]interface{}{"name": "Bob"}},
			wantErr:  true,
		},
		{
			name:     "array exact match",
			expected: []interface{}{"a", "b"},
			actual:   []interface{}{"a", "b"},
			wantErr:  false,
		},
		{
			name:     "array length mismatch",
			expected: []interface{}{"a", "b"},
			actual:   []interface{}{"a"},
			wantErr:  true,
		},
		{
			name:     "array element mismatch",
			expected: []interface{}{"a", "b"},
			actual:   []interface{}{"a", "c"},
			wantErr:  true,
		},
		{
			name:     "expected object actual not object",
			expected: map[string]interface{}{"a": "1"},
			actual:   "not-an-object",
			wantErr:  true,
		},
		{
			name:     "nil actual with non-nil expected scalar",
			expected: "hello",
			actual:   nil,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CompareJSON(tt.expected, tt.actual)
			if (err != nil) != tt.wantErr {
				t.Errorf("CompareJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
