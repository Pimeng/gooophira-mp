package agentupload

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/Pimeng/gooophira-mp/internal/common/agentproto"
	"github.com/Pimeng/gooophira-mp/internal/core/replay"
)

type QueryHandler struct{ Store *Store }

func (h QueryHandler) Handle(ctx context.Context, request agentproto.QueryRequest) (agentproto.QueryResponse, bool) {
	if request.Method != agentproto.QueryReplayUpload && request.Method != agentproto.QueryReplayAutoConfig {
		return agentproto.QueryResponse{}, false
	}
	if h.Store == nil {
		return queryJSON(request.ID, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "replay-upload-unavailable"}), true
	}
	switch request.Method {
	case agentproto.QueryReplayUpload:
		var params agentproto.ReplayUploadParams
		if json.Unmarshal(request.Params, &params) != nil {
			return queryJSON(request.ID, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad-request"}), true
		}
		id, err := replay.ParseID(params.ReplayID)
		if err != nil {
			return queryJSON(request.ID, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad-request"}), true
		}
		result, err := h.Store.Upload(ctx, params.ReplayID, params.Visible)
		if err != nil {
			return queryJSON(request.ID, http.StatusInternalServerError, map[string]any{"ok": false, "error": "upload-failed"}), true
		}
		return queryJSON(request.ID, http.StatusOK, map[string]any{"ok": true, "userId": id.UserID, "chartId": id.ChartID, "recordId": result.RecordID, "scoreId": result.ScoreID, "message": "upload-success"}), true
	default:
		var params agentproto.ReplayAutoConfigParams
		if json.Unmarshal(request.Params, &params) != nil || params.UserID < 0 {
			return queryJSON(request.ID, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad-request"}), true
		}
		show, err := h.Store.AutoConfig(params.UserID, params.Show)
		if err != nil {
			return queryJSON(request.ID, http.StatusInternalServerError, map[string]any{"ok": false, "error": "config-failed"}), true
		}
		return queryJSON(request.ID, http.StatusOK, map[string]any{"ok": true, "userId": params.UserID, "show": show, "shareStationConfigured": true, "autoUploadEnabled": h.Store.cfg.AutoUpload}), true
	}
}

func queryJSON(id string, status int, value any) agentproto.QueryResponse {
	body, _ := json.Marshal(value)
	return agentproto.QueryResponse{ID: id, StatusCode: status, Body: body}
}
