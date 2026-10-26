package host

import "testing"

type countingSettings struct {
	memSettings
	writes int
}

func (s *countingSettings) SetValue(key string, value any) error {
	s.writes++
	return s.memSettings.SetValue(key, value)
}

func TestSetValueIfChangedSkipsUnchanged(t *testing.T) {
	s := &countingSettings{memSettings: *newMemSettings()}
	steps := []struct {
		value      any
		wantWrites int
	}{
		{"page-2", 1},
		{"page-2", 1}, // unchanged: no rewrite
		{"page-3", 2},
		{[]string{"a", "b"}, 3},
		{[]string{"a", "b"}, 3},
		{[]byte{1, 2}, 4},
		{[]byte{1, 2}, 4},
		{nil, 5},
		{nil, 5}, // already absent
	}
	for i, st := range steps {
		if err := setValueIfChanged(s, "src.cursor", st.value); err != nil {
			t.Fatal(err)
		}
		if s.writes != st.wantWrites {
			t.Fatalf("step %d (%v): writes = %d, want %d", i, st.value, s.writes, st.wantWrites)
		}
	}
}
