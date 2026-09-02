package computer

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
)

// ContainerPrefix names every managed container. The suffix is a digest of
// the agent slug, never the slug itself, so a renamed or odd slug can neither
// collide with nor escape the managed namespace.
const ContainerPrefix = "gawkbot-computer"

// Target is one agent's sandbox identity: the container name, the durable
// workspace directory bind-mounted into it, and a stable key for leases.
type Target struct {
	// Key is a non-secret identity used for leases, caches, and locks.
	Key string `json:"key"`
	// Slug is the agent slug the target was derived from.
	Slug          string `json:"slug"`
	ContainerName string `json:"container_name"`
	// WorkspaceDir is the host directory mounted at WorkspaceGuest.
	WorkspaceDir string `json:"workspace_dir"`
	// Label is written to the container so a later inspect can prove the
	// container belongs to this agent.
	Label string `json:"label"`
}

// TargetFor derives an agent's target from its slug and the computers root
// directory (normally <runtime home>/.wuphf/computers).
func TargetFor(slug, computersRoot string) Target {
	digest := sha256.Sum256([]byte(slug))
	full := hex.EncodeToString(digest[:])
	short := full[:16]
	return Target{
		Key:           "agent:" + full,
		Slug:          slug,
		ContainerName: ContainerPrefix + "-" + short,
		WorkspaceDir:  filepath.Join(computersRoot, short),
		Label:         full,
	}
}
