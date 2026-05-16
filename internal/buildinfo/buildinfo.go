package buildinfo

var (
	Version   = "dev"
	Commit    = "none"
	BuildTime = "unknown"
)

type Info struct {
	Version   string
	Commit    string
	BuildTime string
}

func Current() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		BuildTime: BuildTime,
	}
}
