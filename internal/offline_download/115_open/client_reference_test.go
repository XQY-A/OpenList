package _115_open

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	driver115 "github.com/OpenListTeam/OpenList/v4/drivers/115_open"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/offline_download/tool"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestOfflineReferenceValidationDistinguishesStorageAndDatabaseFailures(t *testing.T) {
	dir := t.TempDir()
	d, err := gorm.Open(sqlite.Open(filepath.Join(dir, "references.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	previousConfig := conf.Conf
	conf.Conf = conf.DefaultConfig(dir)
	db.Init(d)
	t.Cleanup(func() {
		conf.Conf = previousConfig
		sqlDB, err := d.DB()
		if err != nil {
			t.Error(err)
			return
		}
		if err := sqlDB.Close(); err != nil {
			t.Error(err)
		}
	})

	for _, test := range []struct {
		name     string
		storage  *model.Storage
		reason   string
		queryErr error
	}{
		{name: "deleted storage", reason: "storage_missing"},
		{name: "disabled storage", storage: &model.Storage{Driver: tool.Open115Name, Disabled: true}, reason: "storage_disabled"},
		{name: "unloaded storage", storage: &model.Storage{Driver: tool.Open115Name}, reason: "storage_unavailable"},
		{name: "changed driver", storage: &model.Storage{Driver: "Local"}, reason: "storage_changed"},
		{name: "database failure", reason: "storage_unavailable", queryErr: errors.New("database temporarily unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			reference := tool.Open115Reference{StorageID: 10000, AccountID: "saved-account", InfoHash: "saved-hash"}
			if test.storage != nil {
				test.storage.MountPath = "/reference-fixture/" + test.name
				if err := db.CreateStorage(test.storage); err != nil {
					t.Fatal(err)
				}
				reference.StorageID = test.storage.ID
			}
			if test.queryErr != nil {
				if err := d.Callback().Query().Before("gorm:query").Register("test:reference_failure", func(tx *gorm.DB) {
					if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Storage" {
						tx.AddError(test.queryErr)
					}
				}); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := d.Callback().Query().Remove("test:reference_failure"); err != nil {
						t.Error(err)
					}
				})
			}
			err := (&Open115{}).ValidateReference(reference)
			var referenceErr *tool.Open115ReferenceError
			if !errors.As(err, &referenceErr) || referenceErr.Reason != test.reason {
				t.Fatalf("binding error = %v, want reason %q", err, test.reason)
			}
			if errors.Is(err, tool.ErrTaskConflict) != (test.queryErr == nil) {
				t.Fatalf("database and binding errors have the same classification: %v", err)
			}
			if test.queryErr != nil && !errors.Is(err, test.queryErr) {
				t.Fatalf("database cause was lost: %v", err)
			}
		})
	}
}

func TestOfflineReferenceErrorPreservesAccountRaceAndCancellation(t *testing.T) {
	err := offlineReferenceError(fmt.Errorf("driver account changed while waiting: %w", driver115.ErrOfflineAccountChanged))
	var referenceErr *tool.Open115ReferenceError
	if !errors.As(err, &referenceErr) || referenceErr.Reason != "account_changed" || !errors.Is(err, tool.ErrTaskConflict) || !errors.Is(err, driver115.ErrOfflineAccountChanged) {
		t.Fatalf("account replacement lost its structured conflict: %v", err)
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("network failure")} {
		err := offlineReferenceError(cause)
		if !errors.Is(err, cause) || errors.Is(err, tool.ErrTaskConflict) {
			t.Fatalf("request failure became an account conflict: %v", err)
		}
	}
	if err := offlineReferenceError(nil); err != nil {
		t.Fatalf("successful request gained an error: %v", err)
	}
}
