package tool

// Open115TaskInfo is the public receipt of an asynchronous 115 Open download.
// Account IDs and storage bindings are deliberately kept out of the response.
type Open115TaskInfo struct {
	Tool                         string `json:"tool"`
	InfoHash                     string `json:"info_hash"`
	SubmissionState              string `json:"submission_state"`
	CloudDeleteState             string `json:"cloud_delete_state"`
	CanDeleteCloudRecord         bool   `json:"can_delete_cloud_record"`
	CloudDeleteUnavailableReason string `json:"cloud_delete_unavailable_reason"`
}

// Get115TaskInfo copies the hash and submission state together, so an API
// response cannot pair a newly accepted state with an older, empty receipt.
// Other download tools keep their existing response format.
func (t *DownloadTask) Get115TaskInfo() *Open115TaskInfo {
	t.mu.RLock()
	if !t.Is115Open() {
		t.mu.RUnlock()
		return nil
	}
	info := &Open115TaskInfo{
		Tool: Open115Name, SubmissionState: SubmissionLegacy,
		CloudDeleteState: t.cloudDeleteStateLocked(),
	}
	var reference Open115Reference
	if t.open115 != nil {
		info.InfoHash = t.open115.InfoHash
		info.SubmissionState = t.open115.SubmissionState
		reference = Open115Reference{t.open115.StorageID, t.open115.AccountID, t.open115.InfoHash}
	}
	allowedErr := t.cloudDeleteAllowedLocked()
	t.mu.RUnlock()
	if allowedErr != nil {
		info.CloudDeleteUnavailableReason = allowedErr.Reason
		return info
	}
	// This is an advisory check of locally available storage/account state.
	// The operation repeats validation under its locks before querying 115.
	client, err := t.durable115Tool()
	if err == nil {
		err = client.ValidateReference(reference)
	}
	if err != nil {
		info.CloudDeleteUnavailableReason = cloudDeleteReferenceError(err).Reason
		return info
	}
	info.CanDeleteCloudRecord = true
	return info
}
