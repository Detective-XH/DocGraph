package workspace

// EntityStats aggregates entity graph counts across all workspace projects.
type EntityStats struct {
	TotalEntities int
	TotalMentions int
}

// GetEntityStats fan-outs entity stats across all projects.
// Projects that return errors are skipped (consistent with GetAllStats pattern).
func (w *Workspace) GetEntityStats() (EntityStats, error) {
	var agg EntityStats
	for _, p := range w.Projects {
		entities, mentions, err := p.Store.Entity.GetEntityStats()
		if err != nil {
			continue
		}
		agg.TotalEntities += entities
		agg.TotalMentions += mentions
	}
	return agg, nil
}
