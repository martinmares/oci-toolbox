package springconfig

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path"
	"strings"
)

const maxSVGConfigs = 1000

type svgClasspathGroup struct {
	entry   ClasspathEntry
	configs []ConfigFile
}

func writeManifestSVG(target string, manifest *Manifest) error {
	groups := manifestSVGGroups(manifest)
	groupsWithConfigs := len(groups)
	configsShown := 0
	for index := range groups {
		remaining := maxSVGConfigs - configsShown
		if remaining <= 0 {
			groups = groups[:index]
			break
		}
		if len(groups[index].configs) > remaining {
			groups[index].configs = groups[index].configs[:remaining]
		}
		configsShown += len(groups[index].configs)
	}

	const (
		width        = 1600
		topHeight    = 174
		groupHeight  = 64
		configHeight = 72
		bottomHeight = 58
	)
	height := topHeight + bottomHeight
	for _, group := range groups {
		height += groupHeight + len(group.configs)*configHeight
	}

	directoryConfigs := 0
	archiveConfigs := 0
	for _, config := range manifest.Configs {
		if config.ArchiveEntry == "" {
			directoryConfigs++
		} else {
			archiveConfigs++
		}
	}
	emptyClasspath := len(manifest.Classpath) - groupsWithConfigs
	omittedGroups := groupsWithConfigs - len(groups)
	omittedConfigs := len(manifest.Configs) - configsShown

	var output bytes.Buffer
	fmt.Fprintf(
		&output,
		`<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-labelledby="title description">
  <title id="title">Spring configuration map</title>
  <desc id="description">Image, classpath entries, and discovered Spring configuration files</desc>
  <style>
    text { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; letter-spacing: 0; }
    .title { font-family: ui-sans-serif, system-ui, sans-serif; font-size: 25px; font-weight: 700; fill: #ffffff; }
    .subtitle { font-size: 14px; fill: #dce7f5; }
    .summary { font-family: ui-sans-serif, system-ui, sans-serif; font-size: 14px; fill: #243447; }
    .group-title { font-size: 15px; font-weight: 700; }
    .group-path { font-size: 13px; fill: #35465a; }
    .config-title { font-size: 14px; font-weight: 700; fill: #1c2733; }
    .config-detail { font-size: 12px; fill: #526170; }
    .footer { font-family: ui-sans-serif, system-ui, sans-serif; font-size: 13px; fill: #526170; }
  </style>
  <rect width="%d" height="%d" fill="#f6f8fa"/>
  <rect x="0" y="0" width="%d" height="104" fill="#17324d"/>
  <text class="title" x="32" y="40">Spring configuration map</text>
  <text class="subtitle" x="32" y="68">%s</text>
  <text class="subtitle" x="32" y="90">digest %s · %s · main %s</text>
  <rect x="32" y="122" width="1536" height="38" rx="6" fill="#ffffff" stroke="#cbd5df"/>
  <text class="summary" x="50" y="146">%d configurations · %d direct files · %d JAR entries · %d/%d classpath entries shown</text>
`,
		width,
		height,
		width,
		height,
		width,
		height,
		width,
		svgText(truncateMiddle(manifest.Image.Reference, 170)),
		svgText(manifest.Image.Digest),
		svgText(manifest.Image.Platform),
		svgText(truncateMiddle(manifest.JIB.MainClass, 90)),
		len(manifest.Configs),
		directoryConfigs,
		archiveConfigs,
		len(groups),
		len(manifest.Classpath),
	)

	y := topHeight
	for _, group := range groups {
		isJar := group.entry.Kind == "jar"
		fill, stroke, label := "#eaf7f0", "#1f7a55", "DIRECTORY"
		if isJar {
			fill, stroke, label = "#fff3dc", "#a45f00", "JAR"
		}
		fmt.Fprintf(
			&output,
			`  <line x1="48" y1="%d" x2="48" y2="%d" stroke="#98a6b5" stroke-width="2"/>
  <circle cx="48" cy="%d" r="5" fill="%s"/>
  <rect x="68" y="%d" width="1500" height="52" rx="6" fill="%s" stroke="%s"/>
  <text class="group-title" x="88" y="%d" fill="%s">CLASSPATH [%04d] %s · %d configurations</text>
  <text class="group-path" x="88" y="%d">%s</text>
`,
			y+26,
			y+groupHeight+len(group.configs)*configHeight,
			y+26,
			stroke,
			y,
			fill,
			stroke,
			y+21,
			stroke,
			group.entry.Index,
			label,
			len(group.configs),
			y+42,
			svgText(truncateMiddle(group.entry.Path, 175)),
		)
		y += groupHeight

		for _, config := range group.configs {
			name := path.Base(config.Source)
			sourceLabel := "image file"
			if config.ArchiveEntry != "" {
				name = path.Base(config.ArchiveEntry)
				sourceLabel = "JAR entry"
			}
			fmt.Fprintf(
				&output,
				`  <line x1="88" y1="%d" x2="116" y2="%d" stroke="#b4bec8" stroke-width="2"/>
  <circle cx="116" cy="%d" r="4" fill="#4f6f8f"/>
  <rect x="132" y="%d" width="1436" height="62" rx="4" fill="#ffffff" stroke="#d8e0e7"/>
  <text class="config-title" x="150" y="%d">%s · %s · %s</text>
  <text class="config-detail" x="150" y="%d">source: %s</text>
  <text class="config-detail" x="150" y="%d">output: %s</text>
`,
				y+31,
				y+31,
				y+31,
				y,
				y+19,
				svgText(config.ID),
				svgText(sourceLabel),
				svgText(truncateMiddle(name, 105)),
				y+38,
				svgText(truncateMiddle(config.Source, 175)),
				y+55,
				svgText(truncateMiddle(config.Output, 175)),
			)
			y += configHeight
		}
	}

	footer := fmt.Sprintf("%d classpath entries without discovered configurations are omitted.", emptyClasspath)
	if omittedConfigs > 0 {
		footer += fmt.Sprintf(
			" The map limit omitted %d configurations in %d classpath groups; manifest.yml remains complete.",
			omittedConfigs,
			omittedGroups,
		)
	}
	fmt.Fprintf(
		&output,
		`  <text class="footer" x="32" y="%d">%s</text>
</svg>
`,
		height-24,
		svgText(footer),
	)

	if err := os.WriteFile(target, output.Bytes(), 0o600); err != nil {
		return fmt.Errorf("writing analysis SVG map: %w", err)
	}
	return nil
}

func manifestSVGGroups(manifest *Manifest) []svgClasspathGroup {
	configsByIndex := make(map[int][]ConfigFile)
	for _, config := range manifest.Configs {
		configsByIndex[config.ClasspathIndex] = append(configsByIndex[config.ClasspathIndex], config)
	}
	groups := make([]svgClasspathGroup, 0, len(configsByIndex))
	for _, entry := range manifest.Classpath {
		configs := configsByIndex[entry.Index]
		if len(configs) == 0 {
			continue
		}
		groups = append(groups, svgClasspathGroup{entry: entry, configs: configs})
	}
	return groups
}

func svgText(value string) string {
	var valid strings.Builder
	for _, r := range value {
		if r == '\n' || r == '\r' || r == '\t' ||
			(r >= 0x20 && r <= 0xd7ff) ||
			(r >= 0xe000 && r <= 0xfffd) ||
			(r >= 0x10000 && r <= 0x10ffff) {
			valid.WriteRune(r)
		}
	}
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(valid.String()))
	return escaped.String()
}

func truncateMiddle(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	if maximum <= 3 {
		return strings.Repeat(".", maximum)
	}
	left := (maximum - 3) / 2
	right := maximum - left - 3
	return string(runes[:left]) + "..." + string(runes[len(runes)-right:])
}
