package takesreply

import "testing"

func TestDatabaseRevisionBounds(t *testing.T) {
	tests := []struct {
		name    string
		input   int
		want    int32
		wantErr bool
	}{
		{name: "zero", input: 0, want: 0},
		{name: "maximum database revision", input: maxDatabaseRevision, want: int32(maxDatabaseRevision)},
		{name: "negative", input: -1, wantErr: true},
		{name: "past database maximum", input: maxDatabaseRevision + 1, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := databaseRevision(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("databaseRevision(%d) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Fatalf("databaseRevision(%d) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestNextDatabaseRevisionBounds(t *testing.T) {
	tests := []struct {
		name    string
		input   int32
		want    int32
		wantErr bool
	}{
		{name: "advance", input: 0, want: 1},
		{name: "maximum valid next revision", input: int32(maxDatabaseRevision - 1), want: int32(maxDatabaseRevision)},
		{name: "negative current revision", input: -1, wantErr: true},
		{name: "cannot overflow database revision", input: int32(maxDatabaseRevision), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := nextDatabaseRevision(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("nextDatabaseRevision(%d) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Fatalf("nextDatabaseRevision(%d) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
