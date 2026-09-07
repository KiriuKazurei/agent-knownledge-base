package storage

import (
	"context"
	"encoding/json"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

const librarySelectColumns = "id,name,description,icon,color,tags_json,favorite,allow_remote_models,auto_summarize_imports,import_conversion_mode,summary_provider_id,auto_review_agent_submissions,review_provider_id,created_at,updated_at"

func scanLibrary(row rowScanner) (model.Library, error) {
	var item model.Library
	var tags, created, updated string
	var favorite, allow, autoSummarize, autoReview int
	if err := row.Scan(&item.ID, &item.Name, &item.Description, &item.Icon, &item.Color, &tags, &favorite, &allow, &autoSummarize, &item.ImportConversionMode, &item.SummaryProviderID, &autoReview, &item.ReviewProviderID, &created, &updated); err != nil {
		return item, err
	}
	_ = json.Unmarshal([]byte(tags), &item.Tags)
	if item.Tags == nil {
		item.Tags = []string{}
	}
	item.Favorite = favorite == 1
	item.AllowRemoteModels = allow == 1
	item.AutoSummarizeImports = autoSummarize == 1
	item.ImportConversionMode = normalizeImportConversionMode(item.ImportConversionMode)
	if item.ImportConversionMode == "reference" && item.AutoSummarizeImports {
		item.ImportConversionMode = "enhanced"
	}
	item.AutoReviewAgentSubmissions = autoReview == 1
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	return item, nil
}

func normalizeImportConversionMode(value string) string {
	switch value {
	case "reference", "enhanced", "off":
		return value
	default:
		return "reference"
	}
}

func (s *Store) GetLibrary(ctx context.Context, id string) (model.Library, error) {
	return scanLibrary(s.DB.QueryRowContext(ctx, "SELECT "+librarySelectColumns+" FROM libraries WHERE id=? AND deleted_at IS NULL", id))
}
