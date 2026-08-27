package storage

import (
	"context"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

func (s *Store) GetLibrary(ctx context.Context, id string) (model.Library, error) {
	var item model.Library
	var allow, autoReview int
	var created, updated string
	err := s.DB.QueryRowContext(ctx, "SELECT id,name,description,allow_remote_models,auto_review_agent_submissions,review_provider_id,created_at,updated_at FROM libraries WHERE id=?", id).Scan(&item.ID, &item.Name, &item.Description, &allow, &autoReview, &item.ReviewProviderID, &created, &updated)
	if err != nil {
		return item, err
	}
	item.AllowRemoteModels = allow == 1
	item.AutoReviewAgentSubmissions = autoReview == 1
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	return item, nil
}
