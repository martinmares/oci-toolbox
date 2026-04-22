package recipe

// Recipe is the top-level structure parsed from a recipe YAML file.
type Recipe struct {
	Name        string     `yaml:"name"`
	Description string     `yaml:"description"`
	Registries  []Registry `yaml:"registries"`
	Sources     []Source   `yaml:"sources"`
	Steps       []Step     `yaml:"steps"`
}

// Registry holds authentication info for registries referenced inside the recipe.
type Registry struct {
	URL      string `yaml:"url"`
	AuthFile string `yaml:"authfile"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// Source defines a named image reference usable in steps.
type Source struct {
	ID  string `yaml:"id"`
	Ref string `yaml:"ref"`
}

// Step represents a single recipe action.
type Step struct {
	Action string `yaml:"action"`
	Path   string `yaml:"path"`

	// add-file / add-dir
	Src   string `yaml:"src"`
	Dst   string `yaml:"dst"`
	Mode  string `yaml:"mode"`
	Owner string `yaml:"owner"`

	// copy-file-from / extract-file
	From     string `yaml:"from"`
	DstLocal string `yaml:"dst-local"`

	// use-base
	// From field reused

	// unzip-to
	// Src = zip/jar path inside image, Dst = destination dir inside image

	// remove-file / remove-dir
	// Dst field reused as target path

	// set-env
	Key   string `yaml:"key"`
	Value string `yaml:"value"`

	// set-label reuses Key + Value

	// set-entrypoint / set-cmd
	Argv []string `yaml:"value_list"`

	// set-user / set-workdir
	// Value field reused

	// fix-permissions
	SetgidDirs bool `yaml:"setgid-dirs"`

	// replace-file
	FindAll string `yaml:"find-all"` // basename to find and whiteout before adding

	// shadow
	ShadowExtractTo string `yaml:"extract-to"` // where to unpack at container runtime
	ShadowOutput    string `yaml:"output"`     // destination path in image (default /app/app.self)

	// Condition (optional)
	Condition *Condition `yaml:"condition"`
}

// Condition allows skipping a step based on image state.
type Condition struct {
	IfNotExists   string   `yaml:"if-not-exists"`
	IfNotExistsIn []string `yaml:"if-not-exists-in"`
}

// Step fields specific to replace-file:
//   FindAll  string  `yaml:"find-all"`  — reused via the FindAll field in Step
