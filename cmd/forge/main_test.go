package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewResourceNaming(t *testing.T) {
	tests := []struct {
		in                   string
		pascal, snake, table string
	}{
		{"Order", "Order", "order", "orders"},
		{"OrderItem", "OrderItem", "order_item", "order_items"},
		{"order-item", "OrderItem", "order_item", "order_items"},
		{"Category", "Category", "category", "categories"},
		{"Address", "Address", "address", "addresses"},
		{"Day", "Day", "day", "days"},
		{"Box", "Box", "box", "boxes"},
		{"HTTPRoute", "HTTPRoute", "http_route", "http_routes"},
		{"userID", "UserID", "user_id", "user_ids"},
	}
	for _, tt := range tests {
		got := newResource(tt.in)
		if got.Pascal != tt.pascal || got.Snake != tt.snake || got.Table != tt.table {
			t.Errorf("newResource(%q) = %s/%s/%s, want %s/%s/%s", tt.in, got.Pascal, got.Snake, got.Table, tt.pascal, tt.snake, tt.table)
		}
	}
	for _, bad := range []string{"!!!", "", "1st", "_"} {
		if newResource(bad).Snake != "" {
			t.Errorf("newResource(%q) should produce an empty resource", bad)
		}
	}
}

func TestNextMigrationVersion(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"00001_init.sql", "00007_add_things.sql", "embed.go", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := nextMigrationVersion(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "00008" {
		t.Fatalf("next version = %s, want 00008", got)
	}
}
