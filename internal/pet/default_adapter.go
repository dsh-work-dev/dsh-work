package pet

import "context"

// DefaultAdapter routes source entries by their explicit package kind. It
// keeps format parsing out of the catalog and gives dual/native packages a
// first-class adapter seam.
type DefaultAdapter struct {
	Codex  CodexAdapter
	Native DshNativeAdapter
}

func NewDefaultAdapter() DefaultAdapter {
	return DefaultAdapter{Codex: NewCodexAdapter(), Native: NewDshNativeAdapter()}
}

func (a DefaultAdapter) Probe(ctx context.Context, source PackageSource) (ProbeResult, error) {
	if source.Kind == SourceDshPets || source.Entry == "dsh-pet.json" || source.ManifestPath == "dsh-pet.json" {
		return a.Native.Probe(ctx, source)
	}
	return a.Codex.Probe(ctx, source)
}

func (a DefaultAdapter) Load(ctx context.Context, source PackageSource) (PetDefinition, error) {
	if source.Kind == SourceDshPets || source.Entry == "dsh-pet.json" || source.ManifestPath == "dsh-pet.json" {
		return a.Native.Load(ctx, source)
	}
	return a.Codex.Load(ctx, source)
}

func (a DefaultAdapter) loadFromInventory(ctx context.Context, inventory packageInventory) (PetDefinition, packageInventory, error) {
	if inventory.Source.Kind == SourceDshPets || inventory.Source.Entry == "dsh-pet.json" || inventory.Source.ManifestPath == "dsh-pet.json" {
		return a.Native.loadFromInventory(ctx, inventory)
	}
	return a.Codex.loadFromInventory(ctx, inventory)
}
