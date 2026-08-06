package db

import (
	"path/filepath"
	"testing"
)

func TestCheckSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "vohive.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if DB == nil {
		t.Fatal("Init() returned without setting DB")
	}
	var m []map[string]interface{}
	if err := DB.Raw("PRAGMA table_info(devices)").Scan(&m).Error; err != nil {
		t.Fatalf("query devices schema: %v", err)
	}
	if len(m) == 0 {
		t.Fatal("devices schema is empty")
	}
}
