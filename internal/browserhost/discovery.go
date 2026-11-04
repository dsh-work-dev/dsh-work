package browserhost

// Discovery describes installation hints, never a connected profile identity.
type Discovery struct {
	Chrome  bool   `json:"chrome"`
	Edge    bool   `json:"edge"`
	Default string `json:"default"`
}

func Discover() Discovery { return discover() }
