// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package agent

import "github.com/usehm/hm/internal/agent/provider"

// addMessages appends msgs to s's active path.
func addMessages(s *Session, msgs ...provider.Message) {
	for _, m := range msgs {
		s.append(m)
	}
}

// compactAt records a compaction keeping the active path's messages from
// index at.
func compactAt(s *Session, at int, summary string) {
	ids := s.MessageIDs()
	s.addCompaction(Compaction{KeptFrom: ids[at], Summary: summary, Time: now()}, provider.Usage{})
}
