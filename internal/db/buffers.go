package db

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// BufferSettings is the (one-row) configuration for buffer minutes.
//
// v1 honors only TaskHabitBreakMinutes and DecompressionMinutes — both are
// applied as padding by the scheduler so back-to-back blocks have breathing
// room. TravelMinutes is parked as future work (needs per-event location
// awareness to do correctly).
type BufferSettings struct {
	TaskHabitBreakMinutes int
	DecompressionMinutes  int
	TravelMinutes         int
}

// LoadBuffers reads the buffer settings from the setting table. Missing or
// unparsable values default to zero.
func LoadBuffers(ctx context.Context, r *SettingRepo) BufferSettings {
	v, ok, err := r.Get(ctx, SettingBuffers)
	if err != nil || !ok || strings.TrimSpace(v) == "" {
		return BufferSettings{}
	}
	parts := strings.Split(v, ",")
	out := BufferSettings{}
	if len(parts) > 0 {
		out.TaskHabitBreakMinutes = atoiOr(parts[0], 0)
	}
	if len(parts) > 1 {
		out.DecompressionMinutes = atoiOr(parts[1], 0)
	}
	if len(parts) > 2 {
		out.TravelMinutes = atoiOr(parts[2], 0)
	}
	return out
}

// SaveBuffers persists the buffer settings as a single comma-separated row.
func SaveBuffers(ctx context.Context, r *SettingRepo, b BufferSettings) error {
	v := fmt.Sprintf("%d,%d,%d", clampNonNeg(b.TaskHabitBreakMinutes),
		clampNonNeg(b.DecompressionMinutes), clampNonNeg(b.TravelMinutes))
	return r.Set(ctx, SettingBuffers, v)
}

// PaddingMinutes is the universal padding the scheduler should add after every
// busy window. It's the larger of the two scheduler-relevant buffers, since v1
// can't distinguish managed vs non-managed sources cheaply.
func (b BufferSettings) PaddingMinutes() int {
	if b.TaskHabitBreakMinutes > b.DecompressionMinutes {
		return b.TaskHabitBreakMinutes
	}
	return b.DecompressionMinutes
}

func atoiOr(s string, def int) int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return v
}

func clampNonNeg(v int) int {
	if v < 0 {
		return 0
	}
	return v
}
