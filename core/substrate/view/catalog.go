package view

// Catalog maps ecosystem names to their environment contribution
// templates. The View composer consults the catalog when generating
// the env stack: each manifest declares which ecosystems it
// participates in, and the catalog supplies the env-var contributions
// for each.
//
// The catalog is content (not code): adding a new ecosystem is a
// matter of adding entries here, not modifying composer logic. Recipes
// can override entries through their Metadata.Ecosystem field, which
// the substrate provisioner records on the manifest.
type Catalog map[string]EcosystemContribution

// EcosystemContribution describes how one ecosystem contributes to
// the composed environment. Path templates are joined relative to a
// manifest's MountPath: a {"PYTHONPATH": ["/site-packages"]} entry
// for a manifest mounted at "/python-3.13" produces a PYTHONPATH
// entry of "/sylk/views/{view_id}/python-3.13/site-packages".
type EcosystemContribution struct {
	// PathEntries are env-var → relative-path-list contributions.
	// Each relative path is joined with the manifest's MountPath at
	// composition time.
	PathEntries map[string][]string
}

// DefaultEcosystemCatalog is the shipped catalog. Covers the common
// language ecosystems and binary distributions; project-specific
// custom ecosystems can be supplied via ComposeRequest.EcosystemCatalog.
//
// The list is intentionally non-comprehensive — it covers the
// ecosystems substrate-shipped recipes use today, and grows as recipes
// for new ecosystems land. Unknown ecosystems are silently skipped at
// composition time (they don't produce env contributions, but their
// files still reach the View's Layout).
var DefaultEcosystemCatalog = Catalog{
	// Generic binary ecosystem — anything that lives at MountPath/bin
	// and should be on PATH. The vast majority of substrate-installed
	// CLIs and language toolchains use this.
	"binary": {
		PathEntries: map[string][]string{
			"PATH":    {"/bin"},
			"MANPATH": {"/share/man"},
		},
	},
	"system-binary": {
		PathEntries: map[string][]string{
			"PATH":    {"/bin"},
			"MANPATH": {"/share/man"},
		},
	},

	// Language toolchains contribute their own homes.
	"python": {
		PathEntries: map[string][]string{
			"PYTHONPATH": {"/site-packages"},
		},
	},
	"python-runtime": {
		PathEntries: map[string][]string{
			"PATH": {"/bin"},
			// PYTHONHOME points at the runtime's prefix.
			"PYTHONHOME": {"/"},
		},
	},
	"node": {
		PathEntries: map[string][]string{
			"NODE_PATH": {"/node_modules"},
		},
	},
	"node-runtime": {
		PathEntries: map[string][]string{
			"PATH": {"/bin"},
		},
	},
	"rust": {
		PathEntries: map[string][]string{
			"PATH":        {"/bin"},
			"CARGO_HOME":  {"/cargo"},
			"RUSTUP_HOME": {"/rustup"},
		},
	},
	"go": {
		PathEntries: map[string][]string{
			"PATH":       {"/bin"},
			"GOPATH":     {"/go"},
			"GOMODCACHE": {"/go/mod-cache"},
			"GOROOT":     {"/"},
		},
	},
	"ruby": {
		PathEntries: map[string][]string{
			"PATH":    {"/bin"},
			"GEM_PATH": {"/gems"},
			"RUBYLIB":  {"/lib"},
		},
	},
	"java": {
		PathEntries: map[string][]string{
			"JAVA_HOME": {"/"},
			"PATH":      {"/bin"},
			"CLASSPATH": {"/jars"},
		},
	},
	"dotnet": {
		PathEntries: map[string][]string{
			"PATH":         {"/"},
			"DOTNET_ROOT":  {"/"},
		},
	},
	"ocaml": {
		PathEntries: map[string][]string{
			"OCAMLPATH": {"/lib"},
			"PATH":      {"/bin"},
		},
	},
	"erlang": {
		PathEntries: map[string][]string{
			"ERL_LIBS": {"/lib"},
			"PATH":     {"/bin"},
		},
	},
	"elixir": {
		PathEntries: map[string][]string{
			"MIX_PATH": {"/lib"},
			"HEX_HOME": {"/hex"},
			"PATH":     {"/bin"},
		},
	},
	"haskell": {
		PathEntries: map[string][]string{
			"HASKELL_GHC_PATH": {"/ghc"},
			"PATH":             {"/bin"},
		},
	},
	"kotlin": {
		PathEntries: map[string][]string{
			"KOTLIN_HOME": {"/"},
			"PATH":        {"/bin"},
		},
	},

	// C / C++ libraries contribute lib + include + pkg-config paths
	// rather than a language env var.
	"c-library": {
		PathEntries: map[string][]string{
			"LD_LIBRARY_PATH": {"/lib"},
			"PKG_CONFIG_PATH": {"/lib/pkgconfig"},
			"CPATH":           {"/include"},
		},
	},

	// Curated system shared libraries (libssl, libffi, etc.) shipped
	// as their own pseudo-ecosystem to keep cross-ecosystem syslib
	// resolution coherent.
	"syslib": {
		PathEntries: map[string][]string{
			"LD_LIBRARY_PATH": {"/lib"},
			"DYLD_LIBRARY_PATH": {"/lib"},
		},
	},

	// Custom is the catch-all for tools that don't fit a known ecosystem.
	// Recipes that declare ecosystem="custom" get only PATH for their
	// /bin contents — enough for the common case of "I shipped a CLI".
	"custom": {
		PathEntries: map[string][]string{
			"PATH": {"/bin"},
		},
	},
}
