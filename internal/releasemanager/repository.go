package releasemanager

import (
	"context"
	"encoding/json"
	"errors"
)

// These identities stay fixed independently of DefaultRepo. Only this one
// repository lineage is trusted; historical releases keep their original signer.
const LegacyRepo = "arizzi74/Codex-Telegram-Gateway"
const PreviousRepo = "arizzi74/MACT"
const RenamedRepo = "arizzi74/Twarm"
const officialRepositoryID int64 = 1369450599
const minimumRepositoryBridgeTag = "v0.6.21"
const twarmRepositoryBridgeTag = "v0.7.32"

func validateRepositoryBridgeInstall(release *Release) error {
	if release.Repo != RenamedRepo && release.Repo != PreviousRepo {
		return nil
	}
	minimum := minimumRepositoryBridgeTag
	if release.Repo == RenamedRepo {
		minimum = twarmRepositoryBridgeTag
	}
	comparison, err := CompareVersions(release.Tag, minimum)
	if err != nil {
		return err
	}
	if comparison < 0 {
		return errors.New("the selected release predates this repository's updater bridge; install " + minimum + " or newer")
	}
	return nil
}

// Resolve the historical official name before fetching a release. The old name
// now hosts a compatibility repository, so its redirect and repository identity
// cannot establish the identity of the canonical repository anymore.
func (m *Manager) releaseRepository(ctx context.Context, requested string) (string, error) {
	if requested != LegacyRepo && requested != PreviousRepo && requested != RenamedRepo {
		return requested, nil
	}
	// Resolve by immutable ID: before cutover the owner is MACT; afterward it
	// is Twarm. Recreated name-only compatibility repositories cannot take over
	// the canonical identity, and network failures never select a fallback.
	data, err := m.Download(ctx, "https://api.github.com/repositories/1369450599", 1024*1024)
	if err != nil {
		return "", err
	}
	var repository struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
	}
	if err := json.Unmarshal(data, &repository); err != nil || repository.ID != officialRepositoryID || (repository.FullName != RenamedRepo && repository.FullName != PreviousRepo) {
		return "", errors.New("GitHub did not confirm the canonical repository and immutable repository identity")
	}
	return repository.FullName, nil
}

// Keep the previous official repository while readiness is pending. A failed
// start must not commit the rename; the next successful update can resolve it
// again. Fork selections keep their independent repository policy.
func pendingReleaseRepository(l *Layout, release *Release) (string, error) {
	if release.Repo != RenamedRepo && release.Repo != PreviousRepo {
		return release.Repo, nil
	}
	if !FileExists(l.State) {
		if release.RequestedRepo != "" {
			return release.RequestedRepo, nil
		}
		return release.Repo, nil
	}
	settings, err := SavedSettings(l)
	if err != nil {
		return "", err
	}
	if repo, _ := settings["repo"].(string); repo == LegacyRepo || repo == PreviousRepo {
		return repo, nil
	}
	return release.Repo, nil
}
