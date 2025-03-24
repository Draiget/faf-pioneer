package applog

import (
	"context"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"reflect"
	"testing"
)

func TestGetContextFieldsEmpty(t *testing.T) {
	ctx := context.Background()
	fields := getContextFields(ctx)
	assert.Nil(t, fields, "expected no fields")
}

func TestMergeContextFields(t *testing.T) {
	// Test two different sets of fields that has no common keys.
	initial := []zap.Field{zap.String("a", "1"), zap.String("b", "2")}
	ctx := context.WithValue(context.Background(), logContextFieldKey{}, initial)

	newFields := []zap.Field{zap.String("c", "3")}
	merged := mergeContextFields(ctx, newFields...)
	expected := []zap.Field{zap.String("c", "3"), zap.String("a", "1"), zap.String("b", "2")}
	if !reflect.DeepEqual(merged, expected) {
		t.Errorf("unexpected merge result. Expected %v, got %v", expected, merged)
	}

	// Now test override.
	newFields2 := []zap.Field{zap.String("a", "new")}
	merged2 := mergeContextFields(ctx, newFields2...)
	// We should see that field `a` will be overridden.
	expected2 := []zap.Field{zap.String("a", "new"), zap.String("b", "2")}
	if !reflect.DeepEqual(merged2, expected2) {
		t.Errorf("unexpected merge result with override. Expected %v, got %v", expected2, merged2)
	}
}

func TestAddContextFields(t *testing.T) {
	ctx := context.Background()

	// Add fields to context.
	ctx = AddContextFields(ctx, zap.String("a", "1"))
	fields := getContextFields(ctx)
	if len(fields) != 1 {
		t.Errorf("expected 1 field, got %d", len(fields))
	}

	// Override field `a` and add one more field to context.
	ctx = AddContextFields(ctx, zap.String("a", "2"), zap.String("b", "3"))
	fields = getContextFields(ctx)
	if len(fields) != 2 {
		t.Errorf("expected 2 fields after override, got %d", len(fields))
	}

	// Make sure we have correct value for `a` now.
	var bExists = false
	for _, field := range fields {
		if field.Key == "a" && field.String != "2" {
			t.Errorf("expected field 'a' to be '2', got %s", field.String)
			continue
		}
		if field.Key == "b" {
			bExists = true
			if field.String != "3" {
				t.Errorf("expected field 'b' to be '3', got %s", field.String)
			}
		}
	}

	if !bExists {
		t.Errorf("expected field 'b' to be present")
	}
}

func TestFromContext(t *testing.T) {
	// Use observer to verify input data for a logger.
	core, observed := observer.New(zap.DebugLevel)
	testLogger := zap.New(core)
	setLogger(testLogger)

	ctx := AddContextFields(context.Background(), zap.String("x", "y"))
	loggerFromCtx := FromContext(ctx)
	loggerFromCtx.Info("test message")

	entries := observed.All()
	if len(entries) == 0 {
		t.Fatal("expected at least one log entry, got none")
	}

	// Verify that in first log entry we now have field `x` with correct value.
	found := false
	for _, field := range entries[0].Context {
		if field.Key == "x" && field.String == "y" {
			found = true
			break
		}
	}

	if !found {
		t.Error("expected log entry to contain field 'x' with value 'y'")
	}
}
