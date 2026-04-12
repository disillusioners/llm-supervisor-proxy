package database

import (
	"testing"
)

func TestQueryBuilder_NewQueryBuilder(t *testing.T) {
	t.Run("creates builder with SQLite dialect", func(t *testing.T) {
		qb := NewQueryBuilder(SQLite)
		if qb == nil {
			t.Fatal("NewQueryBuilder returned nil")
		}
		if qb.dialect != SQLite {
			t.Errorf("Expected dialect SQLite, got %v", qb.dialect)
		}
	})

	t.Run("creates builder with PostgreSQL dialect", func(t *testing.T) {
		qb := NewQueryBuilder(PostgreSQL)
		if qb == nil {
			t.Fatal("NewQueryBuilder returned nil")
		}
		if qb.dialect != PostgreSQL {
			t.Errorf("Expected dialect PostgreSQL, got %v", qb.dialect)
		}
	})
}

func TestQueryBuilder_Placeholder(t *testing.T) {
	tests := []struct {
		name    string
		dialect Dialect
		index   int
		want    string
	}{
		{"SQLite first placeholder", SQLite, 1, "?"},
		{"SQLite third placeholder", SQLite, 3, "?"},
		{"SQLite hundredth placeholder", SQLite, 100, "?"},
		{"PostgreSQL first placeholder", PostgreSQL, 1, "$1"},
		{"PostgreSQL third placeholder", PostgreSQL, 3, "$3"},
		{"PostgreSQL hundredth placeholder", PostgreSQL, 100, "$100"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qb := NewQueryBuilder(tt.dialect)
			got := qb.Placeholder(tt.index)
			if got != tt.want {
				t.Errorf("Placeholder(%d) = %v, want %v", tt.index, got, tt.want)
			}
		})
	}
}

func TestQueryBuilder_Placeholders(t *testing.T) {
	tests := []struct {
		name    string
		dialect Dialect
		count   int
		want    string
	}{
		{"SQLite zero placeholders", SQLite, 0, ""},
		{"SQLite one placeholder", SQLite, 1, "?"},
		{"SQLite three placeholders", SQLite, 3, "?, ?, ?"},
		{"PostgreSQL zero placeholders", PostgreSQL, 0, ""},
		{"PostgreSQL one placeholder", PostgreSQL, 1, "$1"},
		{"PostgreSQL three placeholders", PostgreSQL, 3, "$1, $2, $3"},
		{"PostgreSQL five placeholders", PostgreSQL, 5, "$1, $2, $3, $4, $5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qb := NewQueryBuilder(tt.dialect)
			got := qb.Placeholders(tt.count)
			if got != tt.want {
				t.Errorf("Placeholders(%d) = %v, want %v", tt.count, got, tt.want)
			}
		})
	}
}

func TestQueryBuilder_Now(t *testing.T) {
	tests := []struct {
		name    string
		dialect Dialect
		want    string
	}{
		{"SQLite uses datetime function", SQLite, "datetime('now')"},
		{"PostgreSQL uses NOW function", PostgreSQL, "NOW()"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qb := NewQueryBuilder(tt.dialect)
			got := qb.Now()
			if got != tt.want {
				t.Errorf("Now() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQueryBuilder_BooleanLiteral(t *testing.T) {
	tests := []struct {
		name    string
		dialect Dialect
		input   bool
		want    interface{}
	}{
		{"SQLite true", SQLite, true, 1},
		{"SQLite false", SQLite, false, 0},
		{"PostgreSQL true", PostgreSQL, true, true},
		{"PostgreSQL false", PostgreSQL, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qb := NewQueryBuilder(tt.dialect)
			got := qb.BooleanLiteral(tt.input)
			if got != tt.want {
				t.Errorf("BooleanLiteral(%v) = %v (%T), want %v (%T)", tt.input, got, got, tt.want, tt.want)
			}
		})
	}
}

func TestBooleanToInt(t *testing.T) {
	tests := []struct {
		name  string
		input bool
		want  int64
	}{
		{"true becomes 1", true, 1},
		{"false becomes 0", false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BooleanToInt(tt.input)
			if got != tt.want {
				t.Errorf("BooleanToInt(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestIntToBoolean(t *testing.T) {
	tests := []struct {
		name  string
		input int64
		want  bool
	}{
		{"0 becomes false", 0, false},
		{"1 becomes true", 1, true},
		{"negative becomes true", -1, true},
		{"large positive becomes true", 100, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IntToBoolean(tt.input)
			if got != tt.want {
				t.Errorf("IntToBoolean(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestQueryBuilder_UpsertConfig(t *testing.T) {
	t.Run("SQLite upsert config", func(t *testing.T) {
		qb := NewQueryBuilder(SQLite)
		got := qb.UpsertConfig()
		expected := `INSERT OR REPLACE INTO configs (id, config_json, updated_at) VALUES (1, ?, datetime('now'))`
		if got != expected {
			t.Errorf("UpsertConfig() = %v, want %v", got, expected)
		}
	})

	t.Run("PostgreSQL upsert config", func(t *testing.T) {
		qb := NewQueryBuilder(PostgreSQL)
		got := qb.UpsertConfig()
		expected := `INSERT INTO configs (id, config_json) VALUES (1, $1) ON CONFLICT (id) DO UPDATE SET config_json = $1, updated_at = NOW()`
		if got != expected {
			t.Errorf("UpsertConfig() = %v, want %v", got, expected)
		}
	})
}

func TestQueryBuilder_SelectConfig(t *testing.T) {
	t.Run("SQLite and PostgreSQL use same query", func(t *testing.T) {
		qbSQLite := NewQueryBuilder(SQLite)
		qbPG := NewQueryBuilder(PostgreSQL)

		gotSQLite := qbSQLite.SelectConfig()
		gotPG := qbPG.SelectConfig()

		expected := `SELECT config_json FROM configs WHERE id = 1`
		if gotSQLite != expected {
			t.Errorf("SQLite SelectConfig() = %v, want %v", gotSQLite, expected)
		}
		if gotPG != expected {
			t.Errorf("PostgreSQL SelectConfig() = %v, want %v", gotPG, expected)
		}
	})
}

func TestQueryBuilder_InsertModel(t *testing.T) {
	t.Run("SQLite insert model uses ? placeholders", func(t *testing.T) {
		qb := NewQueryBuilder(SQLite)
		got := qb.InsertModel()
		// Verify it uses ? placeholders
		if !containsOnly(got, "?") {
			t.Errorf("SQLite InsertModel() should use ? placeholders, got: %v", got)
		}
		// Verify it has INSERT OR REPLACE
		if !contains(got, "INSERT OR REPLACE") {
			t.Errorf("SQLite InsertModel() should use INSERT OR REPLACE")
		}
	})

	t.Run("PostgreSQL insert model uses $N placeholders", func(t *testing.T) {
		qb := NewQueryBuilder(PostgreSQL)
		got := qb.InsertModel()
		// Verify it uses $N placeholders
		if !contains(got, "$1") || !contains(got, "$16") {
			t.Errorf("PostgreSQL InsertModel() should use $N placeholders, got: %v", got)
		}
		// Verify it has ON CONFLICT
		if !contains(got, "ON CONFLICT") {
			t.Errorf("PostgreSQL InsertModel() should use ON CONFLICT")
		}
		// Verify it has 16 columns (for 16 placeholders)
		if countOccurrences(got, "$") != 16 {
			t.Errorf("PostgreSQL InsertModel() should have 16 placeholders, got: %d", countOccurrences(got, "$"))
		}
	})
}

func TestQueryBuilder_UpdateModel(t *testing.T) {
	t.Run("SQLite update model uses ? placeholders", func(t *testing.T) {
		qb := NewQueryBuilder(SQLite)
		got := qb.UpdateModel()
		// Verify it uses ? placeholders
		if !containsOnly(got, "?") {
			t.Errorf("SQLite UpdateModel() should use ? placeholders, got: %v", got)
		}
		// Verify it has UPDATE
		if !contains(got, "UPDATE models SET") {
			t.Errorf("SQLite UpdateModel() should use UPDATE models SET")
		}
		// Verify it has 16 ? placeholders (15 fields + WHERE id = ?)
		if countOccurrences(got, "?") != 16 {
			t.Errorf("SQLite UpdateModel() should have 16 placeholders, got: %d", countOccurrences(got, "?"))
		}
	})

	t.Run("PostgreSQL update model uses $N placeholders", func(t *testing.T) {
		qb := NewQueryBuilder(PostgreSQL)
		got := qb.UpdateModel()
		// Verify it uses $N placeholders
		if !contains(got, "$1") || !contains(got, "$16") {
			t.Errorf("PostgreSQL UpdateModel() should use $N placeholders, got: %v", got)
		}
		// Verify it has 16 placeholders (15 fields + WHERE id = $16)
		if countOccurrences(got, "$") != 16 {
			t.Errorf("PostgreSQL UpdateModel() should have 16 placeholders, got: %d", countOccurrences(got, "$"))
		}
	})
}

func TestQueryBuilder_DeleteModel(t *testing.T) {
	t.Run("SQLite delete model uses ? placeholder", func(t *testing.T) {
		qb := NewQueryBuilder(SQLite)
		got := qb.DeleteModel()
		expected := `DELETE FROM models WHERE id = ?`
		if got != expected {
			t.Errorf("DeleteModel() = %v, want %v", got, expected)
		}
	})

	t.Run("PostgreSQL delete model uses $1 placeholder", func(t *testing.T) {
		qb := NewQueryBuilder(PostgreSQL)
		got := qb.DeleteModel()
		expected := `DELETE FROM models WHERE id = $1`
		if got != expected {
			t.Errorf("DeleteModel() = %v, want %v", got, expected)
		}
	})
}

func TestQueryBuilder_GetModelByID(t *testing.T) {
	t.Run("SQLite uses ? placeholder", func(t *testing.T) {
		qb := NewQueryBuilder(SQLite)
		got := qb.GetModelByID()
		if !contains(got, "WHERE id = ?") {
			t.Errorf("SQLite GetModelByID() should use WHERE id = ?, got: %v", got)
		}
		// Should use coalesce with 0 for internal (SQLite compatibility)
		if !contains(got, "coalesce(internal, 0)") {
			t.Errorf("SQLite GetModelByID() should use coalesce(internal, 0)")
		}
	})

	t.Run("PostgreSQL uses $1 placeholder", func(t *testing.T) {
		qb := NewQueryBuilder(PostgreSQL)
		got := qb.GetModelByID()
		if !contains(got, "WHERE id = $1") {
			t.Errorf("PostgreSQL GetModelByID() should use WHERE id = $1, got: %v", got)
		}
		// Should use coalesce with false for internal (PostgreSQL)
		if !contains(got, "coalesce(internal, false)") {
			t.Errorf("PostgreSQL GetModelByID() should use coalesce(internal, false)")
		}
	})
}

func TestQueryBuilder_GetAllModels(t *testing.T) {
	t.Run("SQLite uses ? placeholder and coalesce with 0", func(t *testing.T) {
		qb := NewQueryBuilder(SQLite)
		got := qb.GetAllModels()
		if !contains(got, "ORDER BY name") {
			t.Errorf("GetAllModels() should have ORDER BY name")
		}
		if !contains(got, "coalesce(internal, 0)") {
			t.Errorf("SQLite GetAllModels() should use coalesce(internal, 0)")
		}
	})

	t.Run("PostgreSQL uses coalesce with false", func(t *testing.T) {
		qb := NewQueryBuilder(PostgreSQL)
		got := qb.GetAllModels()
		if !contains(got, "ORDER BY name") {
			t.Errorf("GetAllModels() should have ORDER BY name")
		}
		if !contains(got, "coalesce(internal, false)") {
			t.Errorf("PostgreSQL GetAllModels() should use coalesce(internal, false)")
		}
	})
}

func TestQueryBuilder_GetEnabledModels(t *testing.T) {
	t.Run("SQLite uses enabled = 1", func(t *testing.T) {
		qb := NewQueryBuilder(SQLite)
		got := qb.GetEnabledModels()
		if !contains(got, "WHERE enabled = 1") {
			t.Errorf("SQLite GetEnabledModels() should use WHERE enabled = 1, got: %v", got)
		}
		if !contains(got, "ORDER BY name") {
			t.Errorf("GetEnabledModels() should have ORDER BY name")
		}
	})

	t.Run("PostgreSQL uses enabled = true", func(t *testing.T) {
		qb := NewQueryBuilder(PostgreSQL)
		got := qb.GetEnabledModels()
		if !contains(got, "WHERE enabled = true") {
			t.Errorf("PostgreSQL GetEnabledModels() should use WHERE enabled = true, got: %v", got)
		}
		if !contains(got, "ORDER BY name") {
			t.Errorf("GetEnabledModels() should have ORDER BY name")
		}
	})
}

// Helper functions for string checking
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func containsOnly(s, substr string) bool {
	// Check that $ is NOT present (PostgreSQL indicator)
	return !containsSubstring(s, "$")
}

func countOccurrences(s, substr string) int {
	count := 0
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			count++
		}
	}
	return count
}
