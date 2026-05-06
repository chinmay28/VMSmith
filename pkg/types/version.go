package types

// BuildInfo is the wire format for the GET /api/v1/version response.
// Values are populated by the linker at build time; see pkg/version.
type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}
