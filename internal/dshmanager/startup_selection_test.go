package dshmanager

import (
	"context"
	"testing"
)

func TestStartupDoesNotSubstituteUnverifiedEnvironment(t *testing.T) {
	for _, component := range []string{"runtime", "node"} {
		t.Run(component, func(t *testing.T) {
			m := newTestManager(t)
			target := *m.configured
			if component == "runtime" {
				target.RuntimeID = "removed-runtime"
			} else {
				target.Node = NodeSelection{Kind: NodeSelectionManaged, InstallationID: "removed-node"}
			}
			request := LaunchRequest{RuntimeID: target.RuntimeID, Node: target.Node, Profile: target.Profile}
			if _, err := m.ResolveLaunch(context.Background(), request); err == nil {
				t.Fatal("explicit selection unexpectedly changed")
			}
			if _, err := m.ResolveStartupLaunch(context.Background(), request); err == nil {
				t.Fatal("startup substituted an unverified environment")
			}
		})
	}
}
