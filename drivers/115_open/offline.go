package _115_open

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	sdk "github.com/OpenListTeam/115-sdk-go"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

// OfflineTaskRejectedError means this attempt is known not to have been
// accepted. Other submission errors may have lost a successful cloud receipt
// and must not be retried by submitting the URL again.
type OfflineTaskRejectedError struct {
	Err error
}

func (e *OfflineTaskRejectedError) Error() string { return e.Err.Error() }
func (e *OfflineTaskRejectedError) Unwrap() error { return e.Err }

// ErrOfflineAccountChanged identifies a lost account binding, including a
// concurrent Drop or failed Init, without requiring callers to parse messages.
var ErrOfflineAccountChanged = errors.New("115 Open offline account changed")

// AccountID identifies the account from the last successful initialization.
// An empty value means initialization has not completed or the driver was dropped.
func (d *Open115) AccountID() string {
	uid := d.accountID.Load()
	if uid <= 0 {
		return ""
	}
	return strconv.FormatInt(uid, 10)
}

// checkOfflineAccount is called with offlineMu held, so Init cannot replace
// the client between this check and the associated cloud request.
func (d *Open115) checkOfflineAccount(accountID string) error {
	current := d.AccountID()
	if current == "" || d.client == nil {
		return fmt.Errorf("%w: account is not initialized", ErrOfflineAccountChanged)
	}
	if accountID == "" || current != accountID {
		return fmt.Errorf("%w: account no longer matches the offline task", ErrOfflineAccountChanged)
	}
	return nil
}

// WithOfflineAccount keeps directory preparation on the verified account while
// preventing Drop or Init from replacing its client. The callback must honor
// ctx and must not call methods that acquire offlineMu, including this method
// and the bound offline task methods.
func (d *Open115) WithOfflineAccount(ctx context.Context, accountID string, callback func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if callback == nil {
		return errors.New("115 Open account operation is required")
	}
	if err := d.offlineMu.RLock(ctx); err != nil {
		return err
	}
	defer d.offlineMu.RUnlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.checkOfflineAccount(accountID); err != nil {
		return err
	}
	return callback()
}

func (d *Open115) OfflineDownload(ctx context.Context, uris []string, dstDir model.Obj) ([]string, error) {
	if err := d.offlineMu.RLock(ctx); err != nil {
		return nil, err
	}
	defer d.offlineMu.RUnlock()
	if err := d.checkOfflineAccount(d.AccountID()); err != nil {
		return nil, err
	}
	if err := d.WaitLimit(ctx); err != nil {
		return nil, err
	}
	return d.client.AddOfflineTaskURIs(ctx, uris, dstDir.GetID())
}

// AddOfflineTask preserves a single URL's receipt, including definite rejection.
// Persistent tasks should use AddOfflineTaskForAccount to retain their binding.
func (d *Open115) AddOfflineTask(ctx context.Context, uri string, dstDir model.Obj) (string, error) {
	return d.AddOfflineTaskForAccount(ctx, d.AccountID(), uri, dstDir)
}

func (d *Open115) AddOfflineTaskForAccount(ctx context.Context, accountID, uri string, dstDir model.Obj) (string, error) {
	if err := d.offlineMu.RLock(ctx); err != nil {
		return "", &OfflineTaskRejectedError{Err: err}
	}
	defer d.offlineMu.RUnlock()
	if err := d.checkOfflineAccount(accountID); err != nil {
		return "", &OfflineTaskRejectedError{Err: err}
	}
	return d.addOfflineTask(ctx, uri, dstDir)
}

func (d *Open115) addOfflineTask(ctx context.Context, uri string, dstDir model.Obj) (string, error) {
	if strings.TrimSpace(uri) == "" || strings.ContainsAny(uri, "\r\n") {
		return "", &OfflineTaskRejectedError{Err: errors.New("exactly one offline URL is required")}
	}
	if dstDir == nil || dstDir.GetID() == "" {
		return "", &OfflineTaskRejectedError{Err: errors.New("offline download directory ID is required")}
	}
	if err := ctx.Err(); err != nil {
		return "", &OfflineTaskRejectedError{Err: err}
	}
	if err := d.WaitLimit(ctx); err != nil {
		return "", &OfflineTaskRejectedError{Err: err}
	}
	// Pointer fields distinguish an explicit refusal from a malformed zero-value
	// item. The SDK's bulk helper discards these individual failure receipts.
	var receipts []struct {
		State    *bool  `json:"state"`
		Code     int64  `json:"code"`
		Message  string `json:"message"`
		InfoHash string `json:"info_hash"`
		URL      string `json:"url"`
	}
	_, err := d.client.AuthRequest(ctx, sdk.ApiAddOffline, http.MethodPost, &receipts, sdk.ReqWithForm(sdk.Form{
		"urls":       uri,
		"wp_path_id": dstDir.GetID(),
	}))
	if err != nil {
		var refusal *sdk.Error
		if errors.As(err, &refusal) && refusal.Code != 0 {
			return "", &OfflineTaskRejectedError{Err: err}
		}
		return "", err
	}
	if len(receipts) != 1 {
		return "", errors.New("115 Open returned no unambiguous offline task receipt")
	}
	receipt := receipts[0]
	if receipt.State == nil || (receipt.URL != "" && receipt.URL != uri) {
		return "", errors.New("115 Open returned an invalid offline task receipt")
	}
	if *receipt.State && strings.TrimSpace(receipt.InfoHash) != "" {
		// The request contains one URL; a unique accepted receipt can omit its
		// URL. Preserve the cloud identifier verbatim rather than deriving it.
		return receipt.InfoHash, nil
	}
	if !*receipt.State && receipt.URL == uri && receipt.InfoHash == "" {
		return "", &OfflineTaskRejectedError{Err: fmt.Errorf("115 Open rejected the offline task (code %d): %s", receipt.Code, receipt.Message)}
	}
	return "", errors.New("115 Open did not return an accepted offline task hash; submission outcome is unknown")
}

func (d *Open115) DeleteOfflineTask(ctx context.Context, infoHash string, deleteFiles bool) error {
	if err := d.offlineMu.RLock(ctx); err != nil {
		return err
	}
	defer d.offlineMu.RUnlock()
	if err := d.checkOfflineAccount(d.AccountID()); err != nil {
		return err
	}
	return d.deleteOfflineTask(ctx, infoHash, deleteFiles)
}

// DeleteOfflineTaskForAccount removes only the cloud download record. It never
// asks 115 to remove downloaded files.
func (d *Open115) DeleteOfflineTaskForAccount(ctx context.Context, accountID, infoHash string) error {
	if err := d.offlineMu.RLock(ctx); err != nil {
		return err
	}
	defer d.offlineMu.RUnlock()
	if err := d.checkOfflineAccount(accountID); err != nil {
		return err
	}
	return d.deleteOfflineTask(ctx, infoHash, false)
}

func (d *Open115) deleteOfflineTask(ctx context.Context, infoHash string, deleteFiles bool) error {
	if strings.TrimSpace(infoHash) == "" {
		return errors.New("offline task hash is required")
	}
	if err := d.WaitLimit(ctx); err != nil {
		return err
	}
	return d.client.DeleteOfflineTask(ctx, infoHash, deleteFiles)
}

func (d *Open115) OfflineList(ctx context.Context) (*sdk.OfflineTaskListResp, error) {
	if err := d.offlineMu.RLock(ctx); err != nil {
		return nil, err
	}
	defer d.offlineMu.RUnlock()
	if err := d.checkOfflineAccount(d.AccountID()); err != nil {
		return nil, err
	}
	return d.offlineListPage(ctx, 1)
}

func (d *Open115) offlineListPage(ctx context.Context, page int64) (*sdk.OfflineTaskListResp, error) {
	if err := d.WaitLimit(ctx); err != nil {
		return nil, err
	}
	// SDK v0.2.6 sends page as GET form data, which Resty omits. Use a query
	// parameter so requests beyond page one actually advance through the list.
	var resp sdk.OfflineTaskListResp
	_, err := d.client.AuthRequest(ctx, sdk.ApiOfflineList, http.MethodGet, &resp, sdk.ReqWithQuery(sdk.Form{
		"page": strconv.FormatInt(page, 10),
	}))
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetOfflineTask returns nil only after a complete, consistent list proves the
// hash is absent. A changed or malformed page must not authorize cloud cleanup.
func (d *Open115) GetOfflineTask(ctx context.Context, infoHash string) (*sdk.OfflineTask, error) {
	return d.GetOfflineTaskForAccount(ctx, d.AccountID(), infoHash)
}

func (d *Open115) GetOfflineTaskForAccount(ctx context.Context, accountID, infoHash string) (*sdk.OfflineTask, error) {
	if err := d.offlineMu.RLock(ctx); err != nil {
		return nil, err
	}
	defer d.offlineMu.RUnlock()
	if err := d.checkOfflineAccount(accountID); err != nil {
		return nil, err
	}
	return findOfflineTask(ctx, infoHash, d.offlineListPage)
}

func findOfflineTask(ctx context.Context, infoHash string, listPage func(context.Context, int64) (*sdk.OfflineTaskListResp, error)) (*sdk.OfflineTask, error) {
	if strings.TrimSpace(infoHash) == "" {
		return nil, errors.New("offline task hash is required")
	}
	seen := make(map[string]struct{})
	var count, pageCount int
	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resp, err := listPage(ctx, int64(page))
		if err != nil {
			return nil, fmt.Errorf("failed to query 115 Open offline page %d: %w", page, err)
		}
		if resp == nil || resp.Page != page || resp.Count < 0 || resp.PageCount < 0 {
			return nil, fmt.Errorf("115 Open returned invalid offline pagination at page %d", page)
		}
		if page == 1 {
			count, pageCount = resp.Count, resp.PageCount
		} else if resp.Count != count || resp.PageCount != pageCount {
			return nil, errors.New("115 Open offline list changed during pagination; retry the query")
		}
		if count == 0 {
			if page == 1 && pageCount <= 1 && len(resp.Tasks) == 0 {
				return nil, nil
			}
			return nil, errors.New("115 Open returned inconsistent empty offline pagination")
		}
		if pageCount < page || pageCount > count || len(resp.Tasks) == 0 {
			return nil, fmt.Errorf("115 Open returned incomplete offline pagination at page %d", page)
		}
		var found *sdk.OfflineTask
		for _, item := range resp.Tasks {
			if item.InfoHash == "" {
				return nil, errors.New("115 Open returned an offline task without a hash")
			}
			if _, duplicate := seen[item.InfoHash]; duplicate {
				return nil, errors.New("115 Open repeated an offline task during pagination; retry the query")
			}
			seen[item.InfoHash] = struct{}{}
			if item.InfoHash == infoHash {
				found = &item
			}
		}
		if len(seen) > count || (page == pageCount && len(seen) != count) {
			return nil, errors.New("115 Open offline task count does not match its pages")
		}
		if found != nil {
			return found, nil
		}
		if page == pageCount {
			return nil, nil
		}
	}
}
