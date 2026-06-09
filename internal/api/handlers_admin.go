package api

import "net/http"

func (s *Server) handleAdminLogs(w http.ResponseWriter, _ *http.Request) {
	if s.logBuf == nil {
		writeJSON(w, map[string]any{"logs": []any{}})
		return
	}
	entries := s.logBuf.Entries()
	// Return newest-first so the UI can show most recent at top without reversing.
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	writeJSON(w, map[string]any{"logs": entries})
}
