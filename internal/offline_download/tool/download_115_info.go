package tool

// Open115TaskInfo is the public receipt of an asynchronous 115 Open download.
// Account IDs and storage bindings are deliberately kept out of the response.
type Open115TaskInfo struct {
	Tool            string `json:"tool"`
	InfoHash        string `json:"info_hash"`
	SubmissionState string `json:"submission_state"`
}

// Get115TaskInfo copies the hash and submission state together, so an API
// response cannot pair a newly accepted state with an older, empty receipt.
// Other download tools keep their existing response format.
func (t *DownloadTask) Get115TaskInfo() *Open115TaskInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if !t.Is115Open() {
		return nil
	}
	info := &Open115TaskInfo{Tool: Open115Name, SubmissionState: SubmissionLegacy}
	if t.open115 != nil {
		info.InfoHash = t.open115.InfoHash
		info.SubmissionState = t.open115.SubmissionState
	}
	return info
}
