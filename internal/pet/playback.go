package pet

import "strconv"

// Playback contains only validated media coordinates and opaque Host URLs.
// Resource paths and source package metadata never cross this boundary.
type Playback struct {
	Video  bool                  `json:"video"`
	Key    string                `json:"key"`
	Assets map[string]string     `json:"assets"`
	Tracks map[string][]FrameRef `json:"tracks"`
	Look   map[int]FrameRef      `json:"look"`
}

func ProjectPlayback(d PetDefinition, key string) Playback {
	p := Playback{Video: d.Source.Profile == RendererWebM, Key: key, Assets: map[string]string{}, Tracks: map[string][]FrameRef{}, Look: map[int]FrameRef{}}
	assets := map[string]Asset{}
	for i, a := range d.Assets {
		assets[a.ID] = a
		p.Assets[a.ID] = "/__pet_media/" + key + "/" + strconv.Itoa(i)
	}
	normalize := func(t TrackSpec, f FrameRef) FrameRef {
		if f.AssetID == "" {
			f.AssetID = d.Assets[0].ID
		}
		a := assets[f.AssetID]
		if f.Width > 0 && f.Height > 0 {
			return f
		}
		if t.RendererKind == RendererRasterV1 && d.Geometry.Columns <= 1 && d.Geometry.Rows <= 1 {
			f.Width = a.Width
			f.Height = a.Height
			return f
		}
		if d.Geometry.Columns > 0 {
			f.X = (f.Index % d.Geometry.Columns) * d.Geometry.CellWidth
			f.Y = (f.Index / d.Geometry.Columns) * d.Geometry.CellHeight
			f.Width = d.Geometry.CellWidth
			f.Height = d.Geometry.CellHeight
		}
		return f
	}
	for name, t := range d.Tracks {
		for _, f := range t.Frames {
			p.Tracks[name] = append(p.Tracks[name], normalize(t, f))
		}
	}
	if d.Directions != nil {
		for _, dir := range d.Directions.Directions {
			p.Look[dir.FrameIndex] = normalize(TrackSpec{RendererKind: RendererCodexAtlas}, FrameRef{Index: dir.FrameIndex, DurationMS: 1})
		}
	}
	return p
}
