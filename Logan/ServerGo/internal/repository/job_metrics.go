package repository

import (
	"context"
	"fmt"
	"gorm.io/gorm"
)

// JobCounts describes persistent processing backlog. Pending includes retry_wait.
// Completed jobs are intentionally excluded from the backlog snapshot.
type JobCounts struct {
	Kind                     string
	Pending, Running, Failed int64
}

// ProcessingJobSnapshot returns both supported kinds, including zeros when absent.
// It queries one bounded grouped result without reading task IDs or source data.
func ProcessingJobSnapshot(ctx context.Context, db *gorm.DB) ([]JobCounts, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if db == nil {
		return nil, fmt.Errorf("processing job database unavailable")
	}
	var rows []struct {
		Kind, State string
		Count       int64
	}
	err := db.WithContext(ctx).Raw("SELECT kind,state,COUNT(*) AS count FROM logan_processing_job WHERE kind IN ('native','web') AND state IN ('pending','retry_wait','running','failed') GROUP BY kind,state").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := []JobCounts{{Kind: "native"}, {Kind: "web"}}
	for _, row := range rows {
		var dst *JobCounts
		switch row.Kind {
		case "native":
			dst = &out[0]
		case "web":
			dst = &out[1]
		default:
			continue
		}
		switch row.State {
		case "pending", "retry_wait":
			dst.Pending += row.Count
		case "running":
			dst.Running += row.Count
		case "failed":
			dst.Failed += row.Count
		}
	}
	return out, nil
}
