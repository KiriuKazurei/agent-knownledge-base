package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
	"github.com/google/uuid"
)

const (
	defaultGraphWorkspaceName = "默认图谱"
	defaultGraphNodeLimit     = 250
	maximumGraphNodeLimit     = 300
)

var (
	ErrGraphWorkspaceNotFound = errors.New("graph workspace not found")
	ErrGraphNodeNotFound      = errors.New("graph node not found in workspace library")
)

func graphKnowledgeNodeID(uri string, revision int) string {
	return uri + "?revision=" + strconv.Itoa(revision)
}

func graphEdgeID(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:16])
}

func normalizeGraphPercentage(value float64) (float64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
		return 0, errors.New("graph position percentages must be finite numbers between 0 and 100")
	}
	return math.Round(value*1000) / 1000, nil
}

func (s *Store) CreateGraphWorkspace(ctx context.Context, libraryID, name string) (model.GraphWorkspace, error) {
	if _, err := s.GetLibrary(ctx, libraryID); err != nil {
		return model.GraphWorkspace{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultGraphWorkspaceName
	}
	if len([]rune(name)) > 120 {
		return model.GraphWorkspace{}, errors.New("graph workspace name must be at most 120 characters")
	}
	t, stamp := now()
	item := model.GraphWorkspace{ID: uuid.NewString(), LibraryID: libraryID, Name: name, Filters: map[string]any{}, Viewport: map[string]any{}, CreatedAt: t, UpdatedAt: t}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return model.GraphWorkspace{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO graph_workspaces(id,library_id,name,created_at,updated_at) VALUES(?,?,?,?,?)`, item.ID, libraryID, name, stamp, stamp); err != nil {
		return model.GraphWorkspace{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO graph_workspace_preferences(workspace_id,filters_json,viewport_json,updated_at) VALUES(?,'{}','{}',?)`, item.ID, stamp); err != nil {
		return model.GraphWorkspace{}, err
	}
	if err = tx.Commit(); err != nil {
		return model.GraphWorkspace{}, err
	}
	return item, nil
}

func (s *Store) GetGraphWorkspace(ctx context.Context, id string) (model.GraphWorkspace, error) {
	var item model.GraphWorkspace
	var filtersJSON, viewportJSON, created, updated string
	err := s.DB.QueryRowContext(ctx, `SELECT gw.id,gw.library_id,gw.name,COALESCE(gwp.filters_json,'{}'),COALESCE(gwp.viewport_json,'{}'),gw.created_at,gw.updated_at
		FROM graph_workspaces gw JOIN libraries l ON l.id=gw.library_id AND l.deleted_at IS NULL
		LEFT JOIN graph_workspace_preferences gwp ON gwp.workspace_id=gw.id WHERE gw.id=?`, id).
		Scan(&item.ID, &item.LibraryID, &item.Name, &filtersJSON, &viewportJSON, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return model.GraphWorkspace{}, ErrGraphWorkspaceNotFound
	}
	if err != nil {
		return model.GraphWorkspace{}, err
	}
	item.Filters = map[string]any{}
	item.Viewport = map[string]any{}
	if err := json.Unmarshal([]byte(filtersJSON), &item.Filters); err != nil {
		return model.GraphWorkspace{}, fmt.Errorf("decode graph workspace filters: %w", err)
	}
	if err := json.Unmarshal([]byte(viewportJSON), &item.Viewport); err != nil {
		return model.GraphWorkspace{}, fmt.Errorf("decode graph workspace viewport: %w", err)
	}
	item.CreatedAt = parseTime(created)
	item.UpdatedAt = parseTime(updated)
	return item, nil
}

func (s *Store) GetOrCreateDefaultGraphWorkspace(ctx context.Context, libraryID string) (model.GraphWorkspace, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT gw.id FROM graph_workspaces gw JOIN libraries l ON l.id=gw.library_id AND l.deleted_at IS NULL WHERE gw.library_id=? AND gw.name=?`, libraryID, defaultGraphWorkspaceName).Scan(&id)
	if err == nil {
		return s.GetGraphWorkspace(ctx, id)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.GraphWorkspace{}, err
	}
	item, createErr := s.CreateGraphWorkspace(ctx, libraryID, defaultGraphWorkspaceName)
	if createErr == nil {
		return item, nil
	}
	// A concurrent caller may have created the unique default workspace.
	err = s.DB.QueryRowContext(ctx, `SELECT id FROM graph_workspaces WHERE library_id=? AND name=?`, libraryID, defaultGraphWorkspaceName).Scan(&id)
	if err != nil {
		return model.GraphWorkspace{}, createErr
	}
	return s.GetGraphWorkspace(ctx, id)
}

func (s *Store) UpdateGraphWorkspace(ctx context.Context, id string, name *string, filters, viewport *map[string]any) (model.GraphWorkspace, error) {
	current, err := s.GetGraphWorkspace(ctx, id)
	if err != nil {
		return model.GraphWorkspace{}, err
	}
	if name != nil {
		value := strings.TrimSpace(*name)
		if value == "" || len([]rune(value)) > 120 {
			return model.GraphWorkspace{}, errors.New("graph workspace name must contain 1-120 characters")
		}
		current.Name = value
	}
	if filters != nil {
		current.Filters = *filters
	}
	if viewport != nil {
		current.Viewport = *viewport
	}
	filtersJSON, err := json.Marshal(current.Filters)
	if err != nil || len(filtersJSON) > 64*1024 {
		return model.GraphWorkspace{}, errors.New("graph workspace filters must be valid JSON under 64 KiB")
	}
	viewportJSON, err := json.Marshal(current.Viewport)
	if err != nil || len(viewportJSON) > 64*1024 {
		return model.GraphWorkspace{}, errors.New("graph workspace viewport must be valid JSON under 64 KiB")
	}
	updatedAt, stamp := now()
	current.UpdatedAt = updatedAt
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return model.GraphWorkspace{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE graph_workspaces SET name=?,updated_at=? WHERE id=?`, current.Name, stamp, id)
	if err != nil {
		return model.GraphWorkspace{}, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return model.GraphWorkspace{}, ErrGraphWorkspaceNotFound
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO graph_workspace_preferences(workspace_id,filters_json,viewport_json,updated_at) VALUES(?,?,?,?)
		ON CONFLICT(workspace_id) DO UPDATE SET filters_json=excluded.filters_json,viewport_json=excluded.viewport_json,updated_at=excluded.updated_at`, id, string(filtersJSON), string(viewportJSON), stamp); err != nil {
		return model.GraphWorkspace{}, err
	}
	if err = tx.Commit(); err != nil {
		return model.GraphWorkspace{}, err
	}
	return current, nil
}

func (s *Store) graphNodeBelongsToLibrary(ctx context.Context, libraryID, nodeID string) (bool, error) {
	if strings.HasPrefix(nodeID, "kah://knowledge/") {
		knowledgeID, revision, section, err := ParseKnowledgeURI(nodeID)
		if err != nil || revision < 1 || section != "" {
			return false, nil
		}
		var count int
		err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_items ki JOIN libraries l ON l.id=ki.library_id
			JOIN knowledge_revisions kr ON kr.knowledge_id=ki.id AND kr.revision=ki.stable_revision
			WHERE ki.id=? AND ki.library_id=? AND kr.revision=? AND kr.status='stable' AND l.deleted_at IS NULL`, knowledgeID, libraryID, revision).Scan(&count)
		return count == 1, err
	}
	if strings.HasPrefix(nodeID, "kah://document/") {
		documentID, err := ParseDocumentURI(nodeID)
		if err != nil {
			return false, nil
		}
		var count int
		err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents d JOIN libraries l ON l.id=d.library_id
			WHERE d.id=? AND d.library_id=? AND d.deleted_at IS NULL AND l.deleted_at IS NULL`, documentID, libraryID).Scan(&count)
		return count == 1, err
	}
	var count int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_sources ks
		JOIN knowledge_items ki ON ki.id=ks.knowledge_id AND ki.stable_revision=ks.revision
		JOIN knowledge_revisions kr ON kr.knowledge_id=ki.id AND kr.revision=ki.stable_revision AND kr.status='stable'
		JOIN libraries l ON l.id=ki.library_id AND l.deleted_at IS NULL
		WHERE ki.library_id=? AND ks.resource=?`, libraryID, nodeID).Scan(&count)
	if err != nil || count > 0 {
		return count > 0, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_derivations kd
		JOIN knowledge_items ki ON ki.id=kd.knowledge_id AND ki.stable_revision=kd.revision
		JOIN knowledge_revisions kr ON kr.knowledge_id=ki.id AND kr.revision=ki.stable_revision AND kr.status='stable'
		JOIN libraries l ON l.id=ki.library_id AND l.deleted_at IS NULL
		JOIN json_each(kd.derivation_json,'$.premises') premise
		WHERE ki.library_id=? AND premise.value=?`, libraryID, nodeID).Scan(&count)
	return count > 0, err
}

// SaveGraphPositions validates every requested node before beginning the
// transaction. A mixed valid/invalid batch therefore never partially changes
// the saved layout.
func (s *Store) SaveGraphPositions(ctx context.Context, workspaceID string, updates []model.GraphPositionUpdate) ([]model.GraphPositionUpdate, error) {
	workspace, err := s.GetGraphWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	normalized := make([]model.GraphPositionUpdate, len(updates))
	seen := map[string]bool{}
	for index, update := range updates {
		update.NodeID = strings.TrimSpace(update.NodeID)
		if update.NodeID == "" || seen[update.NodeID] {
			return nil, errors.New("graph position node IDs must be non-empty and unique")
		}
		seen[update.NodeID] = true
		if update.XPct, err = normalizeGraphPercentage(update.XPct); err != nil {
			return nil, err
		}
		if update.YPct, err = normalizeGraphPercentage(update.YPct); err != nil {
			return nil, err
		}
		belongs, belongsErr := s.graphNodeBelongsToLibrary(ctx, workspace.LibraryID, update.NodeID)
		if belongsErr != nil {
			return nil, belongsErr
		}
		if !belongs {
			return nil, fmt.Errorf("%w: %s", ErrGraphNodeNotFound, update.NodeID)
		}
		normalized[index] = update
	}
	_, stamp := now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, update := range normalized {
		if _, err = tx.ExecContext(ctx, `INSERT INTO graph_node_positions(workspace_id,node_id,x_pct,y_pct,updated_at) VALUES(?,?,?,?,?)
			ON CONFLICT(workspace_id,node_id) DO UPDATE SET x_pct=excluded.x_pct,y_pct=excluded.y_pct,updated_at=excluded.updated_at`, workspaceID, update.NodeID, update.XPct, update.YPct, stamp); err != nil {
			return nil, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE graph_workspaces SET updated_at=? WHERE id=?`, stamp, workspaceID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return normalized, nil
}

func deterministicGraphPosition(nodeID string) model.GraphPosition {
	stableHash := func(seed uint32) uint32 {
		hash := seed
		for _, value := range []byte(nodeID) {
			hash ^= uint32(value)
			hash *= 16777619
		}
		return hash
	}
	const denominator = float64(^uint32(0))
	return model.GraphPosition{
		XPct: math.Round((8+84*float64(stableHash(2166136261))/denominator)*1000) / 1000,
		YPct: math.Round((8+84*float64(stableHash(3339675911))/denominator)*1000) / 1000,
	}
}

func graphPositionsOverlap(left, right model.GraphPosition, minimumDistance float64) bool {
	x := left.XPct - right.XPct
	y := left.YPct - right.YPct
	return x*x+y*y < minimumDistance*minimumDistance
}

func resolveGraphPositionCollision(base model.GraphPosition, occupied []model.GraphPosition, minimumDistance float64) model.GraphPosition {
	candidate := base
	for attempt := 1; attempt <= 64; attempt++ {
		collides := false
		for _, position := range occupied {
			if graphPositionsOverlap(candidate, position, minimumDistance) {
				collides = true
				break
			}
		}
		if !collides {
			break
		}
		candidate = model.GraphPosition{
			XPct: math.Round((8+math.Mod(base.XPct-8+float64(attempt)*17.371, 84))*1000) / 1000,
			YPct: math.Round((8+math.Mod(base.YPct-8+float64(attempt)*29.117, 84))*1000) / 1000,
		}
	}
	return candidate
}

func (s *Store) AutoLayoutGraph(ctx context.Context, libraryID, workspaceID string, overwrite bool) ([]model.GraphPositionUpdate, error) {
	snapshot, err := s.BuildGraphSnapshot(ctx, libraryID, workspaceID, "", 1, maximumGraphNodeLimit)
	if err != nil {
		return nil, err
	}
	updates := make([]model.GraphPositionUpdate, 0, len(snapshot.Nodes))
	occupied := make([]model.GraphPosition, 0, len(snapshot.Nodes))
	nodeIDs := make([]string, 0, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if node.Position != nil && !overwrite {
			occupied = append(occupied, *node.Position)
			continue
		}
		nodeIDs = append(nodeIDs, node.ID)
	}
	sort.Strings(nodeIDs)
	minimumDistance := math.Max(3, math.Min(12, 50/math.Sqrt(float64(max(len(snapshot.Nodes), 1)))))
	for _, nodeID := range nodeIDs {
		position := resolveGraphPositionCollision(deterministicGraphPosition(nodeID), occupied, minimumDistance)
		occupied = append(occupied, position)
		updates = append(updates, model.GraphPositionUpdate{NodeID: nodeID, XPct: position.XPct, YPct: position.YPct})
	}
	if len(updates) == 0 {
		return []model.GraphPositionUpdate{}, nil
	}
	return s.SaveGraphPositions(ctx, snapshot.Workspace.ID, updates)
}

func (s *Store) graphPositions(ctx context.Context, workspaceID string) (map[string]model.GraphPosition, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT node_id,x_pct,y_pct FROM graph_node_positions WHERE workspace_id=?`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	positions := map[string]model.GraphPosition{}
	for rows.Next() {
		var nodeID string
		var position model.GraphPosition
		if err := rows.Scan(&nodeID, &position.XPct, &position.YPct); err != nil {
			return nil, err
		}
		positions[nodeID] = position
	}
	return positions, rows.Err()
}

func (s *Store) BuildGraphSnapshot(ctx context.Context, libraryID, workspaceID, centerNodeID string, requestedDepth, limit int) (model.GraphSnapshot, error) {
	if requestedDepth < 1 {
		requestedDepth = 1
	}
	appliedDepth := requestedDepth
	if appliedDepth > 2 {
		appliedDepth = 2
	}
	if limit < 1 {
		limit = defaultGraphNodeLimit
	}
	if limit > maximumGraphNodeLimit {
		limit = maximumGraphNodeLimit
	}
	var workspace model.GraphWorkspace
	var err error
	if workspaceID == "" {
		workspace, err = s.GetOrCreateDefaultGraphWorkspace(ctx, libraryID)
	} else {
		workspace, err = s.GetGraphWorkspace(ctx, workspaceID)
		if err == nil && workspace.LibraryID != libraryID {
			return model.GraphSnapshot{}, ErrGraphWorkspaceNotFound
		}
	}
	if err != nil {
		return model.GraphSnapshot{}, err
	}

	rows, err := s.DB.QueryContext(ctx, `SELECT ki.id,kr.revision,kr.payload_json,kr.flags_json
		FROM knowledge_items ki JOIN libraries l ON l.id=ki.library_id AND l.deleted_at IS NULL
		JOIN knowledge_revisions kr ON kr.knowledge_id=ki.id AND kr.revision=ki.stable_revision
		WHERE ki.library_id=? AND kr.status='stable' ORDER BY lower(json_extract(kr.payload_json,'$.title')),ki.id`, libraryID)
	if err != nil {
		return model.GraphSnapshot{}, err
	}
	allNodes := []model.GraphNode{}
	nodeIndexes := map[string]int{}
	knowledgeNodeIDs := map[string]string{}
	knowledgeRevisions := map[string]int{}
	for rows.Next() {
		var id, payloadJSON, flagsJSON string
		var revision int
		if err := rows.Scan(&id, &revision, &payloadJSON, &flagsJSON); err != nil {
			rows.Close()
			return model.GraphSnapshot{}, err
		}
		var payload model.KnowledgePayload
		var flags []string
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			rows.Close()
			return model.GraphSnapshot{}, fmt.Errorf("decode graph knowledge payload: %w", err)
		}
		_ = json.Unmarshal([]byte(flagsJSON), &flags)
		uri := KnowledgeURI(id)
		nodeID := graphKnowledgeNodeID(uri, revision)
		trust := "unverified"
		if len(payload.Verified) > 0 {
			trust = "verified"
		}
		nodeIndexes[nodeID] = len(allNodes)
		knowledgeNodeIDs[uri] = nodeID
		knowledgeRevisions[uri] = revision
		allNodes = append(allNodes, model.GraphNode{ID: nodeID, Kind: "knowledge", Label: payload.Title, Description: payload.Description, URI: uri, Revision: revision, Status: "stable", Trust: trust, Tags: payload.Tags})
	}
	if err := rows.Close(); err != nil {
		return model.GraphSnapshot{}, err
	}

	allEdges := []model.GraphEdge{}
	relationRows, err := s.DB.QueryContext(ctx, `SELECT kr.knowledge_id,kr.revision,r.relation_type,r.target_uri,r.target_revision
		FROM knowledge_relations r JOIN knowledge_revisions kr ON kr.knowledge_id=r.knowledge_id AND kr.revision=r.revision
		JOIN knowledge_items ki ON ki.id=kr.knowledge_id AND ki.stable_revision=kr.revision
		JOIN libraries l ON l.id=ki.library_id AND l.deleted_at IS NULL
		WHERE ki.library_id=? AND kr.status='stable'`, libraryID)
	if err != nil {
		return model.GraphSnapshot{}, err
	}
	for relationRows.Next() {
		var sourceID, relationType, targetURI string
		var sourceRevision, targetRevision int
		if err := relationRows.Scan(&sourceID, &sourceRevision, &relationType, &targetURI, &targetRevision); err != nil {
			relationRows.Close()
			return model.GraphSnapshot{}, err
		}
		sourceNodeID := graphKnowledgeNodeID(KnowledgeURI(sourceID), sourceRevision)
		targetKnowledgeID, uriRevision, _, parseErr := ParseKnowledgeURI(targetURI)
		if parseErr != nil {
			continue
		}
		canonicalTarget := KnowledgeURI(targetKnowledgeID)
		targetNodeID, exists := knowledgeNodeIDs[canonicalTarget]
		if !exists {
			continue
		}
		pinnedRevision := targetRevision
		if uriRevision > 0 {
			pinnedRevision = uriRevision
		}
		if pinnedRevision > 0 && pinnedRevision != knowledgeRevisions[canonicalTarget] {
			continue
		}
		allEdges = append(allEdges, model.GraphEdge{ID: graphEdgeID("relation", sourceNodeID, targetNodeID, relationType, strconv.Itoa(pinnedRevision)), Kind: "relation", RelationType: relationType, Source: sourceNodeID, Target: targetNodeID, TargetRevision: pinnedRevision})
	}
	if err := relationRows.Close(); err != nil {
		return model.GraphSnapshot{}, err
	}

	sourceRows, err := s.DB.QueryContext(ctx, `SELECT ks.knowledge_id,ks.revision,ks.source_id,ks.resource,ks.title,ks.locator_json,ks.snapshot_json,
		COALESCE(d.id,''),COALESCE(d.title,''),COALESCE(d.status,'') FROM knowledge_sources ks
		JOIN knowledge_items ki ON ki.id=ks.knowledge_id AND ki.stable_revision=ks.revision
		JOIN knowledge_revisions kr ON kr.knowledge_id=ki.id AND kr.revision=ki.stable_revision AND kr.status='stable'
		JOIN libraries l ON l.id=ki.library_id AND l.deleted_at IS NULL
		LEFT JOIN documents d ON ks.resource=('kah://document/' || d.id) AND d.library_id=ki.library_id AND d.deleted_at IS NULL
		WHERE ki.library_id=? ORDER BY ks.knowledge_id,ks.source_id`, libraryID)
	if err != nil {
		return model.GraphSnapshot{}, err
	}
	for sourceRows.Next() {
		var knowledgeID, sourceID, resource, title, locatorJSON, snapshotJSON, documentID, documentTitle, documentStatus string
		var revision int
		if err := sourceRows.Scan(&knowledgeID, &revision, &sourceID, &resource, &title, &locatorJSON, &snapshotJSON, &documentID, &documentTitle, &documentStatus); err != nil {
			sourceRows.Close()
			return model.GraphSnapshot{}, err
		}
		kind := "external_source"
		label := title
		if strings.HasPrefix(resource, "kah://document/") {
			if documentID == "" {
				continue
			}
			kind = "document"
			if documentTitle != "" {
				label = documentTitle
			}
		}
		if label == "" {
			label = resource
		}
		if _, exists := nodeIndexes[resource]; !exists {
			nodeIndexes[resource] = len(allNodes)
			allNodes = append(allNodes, model.GraphNode{ID: resource, Kind: kind, Label: label, URI: resource, Status: documentStatus, Tags: []string{}})
		}
		locator := map[string]any{}
		snapshot := map[string]any{}
		_ = json.Unmarshal([]byte(locatorJSON), &locator)
		_ = json.Unmarshal([]byte(snapshotJSON), &snapshot)
		sourceNodeID := graphKnowledgeNodeID(KnowledgeURI(knowledgeID), revision)
		allEdges = append(allEdges, model.GraphEdge{ID: graphEdgeID("source", sourceNodeID, resource, sourceID), Kind: "source", RelationType: "cites", Source: sourceNodeID, Target: resource, SourceID: sourceID, Locator: locator, Snapshot: snapshot})
	}
	if err := sourceRows.Close(); err != nil {
		return model.GraphSnapshot{}, err
	}

	derivationRows, err := s.DB.QueryContext(ctx, `SELECT kd.knowledge_id,kd.revision,kd.derivation_json FROM knowledge_derivations kd
		JOIN knowledge_items ki ON ki.id=kd.knowledge_id AND ki.stable_revision=kd.revision
		JOIN knowledge_revisions kr ON kr.knowledge_id=ki.id AND kr.revision=ki.stable_revision AND kr.status='stable'
		JOIN libraries l ON l.id=ki.library_id AND l.deleted_at IS NULL
		WHERE ki.library_id=? ORDER BY kd.knowledge_id,kd.id`, libraryID)
	if err != nil {
		return model.GraphSnapshot{}, err
	}
	type graphDerivation struct {
		knowledgeID string
		revision    int
		value       model.KnowledgeDerivation
	}
	derivations := []graphDerivation{}
	for derivationRows.Next() {
		var knowledgeID, derivationJSON string
		var revision int
		if err := derivationRows.Scan(&knowledgeID, &revision, &derivationJSON); err != nil {
			derivationRows.Close()
			return model.GraphSnapshot{}, err
		}
		var derivation model.KnowledgeDerivation
		if err := json.Unmarshal([]byte(derivationJSON), &derivation); err != nil {
			derivationRows.Close()
			return model.GraphSnapshot{}, fmt.Errorf("decode graph derivation: %w", err)
		}
		derivations = append(derivations, graphDerivation{knowledgeID: knowledgeID, revision: revision, value: derivation})
	}
	if err := derivationRows.Close(); err != nil {
		return model.GraphSnapshot{}, err
	}
	for _, item := range derivations {
		derivation := item.value
		sourceNodeID := graphKnowledgeNodeID(KnowledgeURI(item.knowledgeID), item.revision)
		for _, rawPremise := range derivation.Premises {
			premise := strings.TrimSpace(rawPremise)
			if premise == "" {
				continue
			}
			targetNodeID := ""
			switch {
			case strings.HasPrefix(premise, "kah://knowledge/"):
				targetKnowledgeID, exactRevision, section, parseErr := ParseKnowledgeURI(premise)
				if parseErr != nil || section != "" {
					continue
				}
				canonical := KnowledgeURI(targetKnowledgeID)
				targetNodeID = knowledgeNodeIDs[canonical]
				if targetNodeID == "" || (exactRevision > 0 && knowledgeRevisions[canonical] != exactRevision) {
					continue
				}
			case strings.HasPrefix(premise, "kah://document/"):
				documentID, parseErr := ParseDocumentURI(premise)
				if parseErr != nil {
					continue
				}
				var title, status string
				if queryErr := s.DB.QueryRowContext(ctx, `SELECT title,status FROM documents WHERE id=? AND library_id=? AND deleted_at IS NULL`, documentID, libraryID).Scan(&title, &status); queryErr != nil {
					if errors.Is(queryErr, sql.ErrNoRows) {
						continue
					}
					return model.GraphSnapshot{}, queryErr
				}
				targetNodeID = DocumentURI(documentID)
				if _, exists := nodeIndexes[targetNodeID]; !exists {
					nodeIndexes[targetNodeID] = len(allNodes)
					allNodes = append(allNodes, model.GraphNode{ID: targetNodeID, Kind: "document", Label: title, URI: targetNodeID, Status: status, Tags: []string{}})
				}
			case strings.HasPrefix(premise, "https://"):
				targetNodeID = premise
				if _, exists := nodeIndexes[targetNodeID]; !exists {
					nodeIndexes[targetNodeID] = len(allNodes)
					allNodes = append(allNodes, model.GraphNode{ID: targetNodeID, Kind: "external_source", Label: premise, URI: premise, Tags: []string{}})
				}
			default:
				continue
			}
			allEdges = append(allEdges, model.GraphEdge{
				ID: graphEdgeID("derivation", sourceNodeID, targetNodeID, derivation.ID), Kind: "derivation", RelationType: "derived-from", Source: sourceNodeID, Target: targetNodeID,
				Locator: map[string]any{"derivationId": derivation.ID, "method": derivation.Method, "limitations": derivation.Limitations, "uncertainty": derivation.Uncertainty},
			})
		}
	}
	centerNodeID = strings.TrimSpace(centerNodeID)
	if centerNodeID != "" {
		if _, exists := nodeIndexes[centerNodeID]; !exists {
			return model.GraphSnapshot{}, ErrGraphNodeNotFound
		}
		visibleAtDepth := map[string]int{centerNodeID: 0}
		frontier := []string{centerNodeID}
		for len(frontier) > 0 {
			current := frontier[0]
			frontier = frontier[1:]
			depth := visibleAtDepth[current]
			if depth >= appliedDepth {
				continue
			}
			for _, edge := range allEdges {
				neighbor := ""
				if edge.Source == current {
					neighbor = edge.Target
				} else if edge.Target == current {
					neighbor = edge.Source
				}
				if neighbor == "" {
					continue
				}
				if _, exists := visibleAtDepth[neighbor]; exists {
					continue
				}
				visibleAtDepth[neighbor] = depth + 1
				frontier = append(frontier, neighbor)
			}
		}
		filteredNodes := make([]model.GraphNode, 0, len(visibleAtDepth))
		for _, node := range allNodes {
			if _, visible := visibleAtDepth[node.ID]; visible {
				filteredNodes = append(filteredNodes, node)
			}
		}
		allNodes = filteredNodes
	}

	positions, err := s.graphPositions(ctx, workspace.ID)
	if err != nil {
		return model.GraphSnapshot{}, err
	}
	hidden := 0
	if len(allNodes) > limit {
		hidden = len(allNodes) - limit
		allNodes = allNodes[:limit]
	}
	visible := map[string]bool{}
	for index := range allNodes {
		visible[allNodes[index].ID] = true
		if position, ok := positions[allNodes[index].ID]; ok {
			copy := position
			allNodes[index].Position = &copy
		}
		if allNodes[index].Tags == nil {
			allNodes[index].Tags = []string{}
		}
	}
	filteredEdges := make([]model.GraphEdge, 0, len(allEdges))
	for _, edge := range allEdges {
		if visible[edge.Source] && visible[edge.Target] {
			filteredEdges = append(filteredEdges, edge)
		}
	}
	sort.Slice(filteredEdges, func(i, j int) bool { return filteredEdges[i].ID < filteredEdges[j].ID })
	return model.GraphSnapshot{Workspace: workspace, Nodes: allNodes, Edges: filteredEdges, Truncated: hidden > 0, HiddenNodes: hidden, Limits: model.GraphLimits{RequestedDepth: requestedDepth, AppliedDepth: appliedDepth, MaxNodes: limit}}, nil
}
