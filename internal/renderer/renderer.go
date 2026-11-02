package renderer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/go-secure-stdlib/parseutil"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"
)

const prefix = "vault.hashicorp.com/"
const marker = "vault-renderer.dadianas.com/managed"
const checksumKey = "vault-renderer.dadianas.com/checksum"

type Config struct {
	Injector struct {
		Enabled             bool              `yaml:"enabled"`
		AnnotationDiscovery bool              `yaml:"annotationDiscovery"`
		Mode                string            `yaml:"mode"`
		Defaults            map[string]string `yaml:"defaults"`
		Targets             []Target          `yaml:"targets"`
		Exclude             []Target          `yaml:"exclude"`
		NamePrefix          string            `yaml:"namePrefix"`
		Labels              map[string]string `yaml:"labels"`
	} `yaml:"vault-injector"`
}

type Target struct {
	Kind               string    `yaml:"kind"`
	Kinds              []string  `yaml:"kinds"`
	Name               string    `yaml:"name"`
	Namespace          string    `yaml:"namespace"`
	LabelSelector      *Selector `yaml:"labelSelector"`
	AnnotationSelector *struct {
		MatchAnnotations map[string]string `yaml:"matchAnnotations"`
	} `yaml:"annotationSelector"`
	Inject map[string]any `yaml:"inject"`
}

type Selector struct {
	MatchLabels      map[string]string `yaml:"matchLabels"`
	MatchExpressions []struct {
		Key      string   `yaml:"key"`
		Operator string   `yaml:"operator"`
		Values   []string `yaml:"values"`
	} `yaml:"matchExpressions"`
}

var supportedKind = map[string]bool{"Deployment": true, "StatefulSet": true, "DaemonSet": true, "Job": true, "CronJob": true, "Pod": true}
var dnsName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
var invalidDNS = regexp.MustCompile(`[^a-z0-9-]`)

func DefaultConfig() Config {
	var cfg Config
	cfg.Injector.Enabled = true
	cfg.Injector.AnnotationDiscovery = true
	return cfg
}

func ParseConfig(data []byte) (Config, error) {
	cfg := DefaultConfig()
	d := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := d.Decode(&document); err != nil {
		return cfg, fmt.Errorf("config: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return cfg, fmt.Errorf("config: expected one YAML document")
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return cfg, fmt.Errorf("config: expected a YAML mapping")
	}
	var injector *yaml.Node
	root := document.Content[0]
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value != "vault-injector" {
			continue
		}
		if injector != nil {
			return cfg, fmt.Errorf("config: duplicate vault-injector root")
		}
		injector = root.Content[i+1]
	}
	if injector != nil && injector.Kind == yaml.AliasNode {
		injector = injector.Alias
	}
	if injector == nil || injector.Kind != yaml.MappingNode {
		return cfg, fmt.Errorf("vault-injector root is required")
	}
	var settings map[string]any
	if err := injector.Decode(&settings); err != nil {
		return cfg, fmt.Errorf("config: %w", err)
	}
	section, err := yaml.Marshal(map[string]any{"vault-injector": settings})
	if err != nil {
		return cfg, fmt.Errorf("config: %w", err)
	}
	strict := yaml.NewDecoder(bytes.NewReader(section))
	strict.KnownFields(true)
	if err := strict.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("config: %w", err)
	}
	if err := validateSettings(cfg.Injector.Defaults); err != nil {
		return cfg, fmt.Errorf("defaults: %w", err)
	}
	if cfg.Injector.Mode != "" && cfg.Injector.Mode != "targets" && cfg.Injector.Mode != "all" {
		return cfg, fmt.Errorf("mode must be targets or all")
	}
	for i, target := range cfg.Injector.Targets {
		if err := validateTarget(target, false); err != nil {
			return cfg, fmt.Errorf("targets[%d]: %w", i, err)
		}
	}
	for i, target := range cfg.Injector.Exclude {
		if err := validateTarget(target, true); err != nil {
			return cfg, fmt.Errorf("exclude[%d]: %w", i, err)
		}
	}
	if cfg.Injector.NamePrefix != "" && (!dnsName.MatchString(cfg.Injector.NamePrefix) || len(cfg.Injector.NamePrefix) > 40) {
		return cfg, fmt.Errorf("namePrefix must be a DNS label of at most 40 characters")
	}
	if _, reserved := cfg.Injector.Labels[marker]; reserved {
		return cfg, fmt.Errorf("labels cannot override %s", marker)
	}
	for key, value := range cfg.Injector.Labels {
		if len(validation.IsQualifiedName(key)) > 0 || len(validation.IsValidLabelValue(value)) > 0 {
			return cfg, fmt.Errorf("invalid Kubernetes label %q", key)
		}
	}
	return cfg, nil
}

func validateTarget(t Target, exclusion bool) error {
	if t.Kind != "" && len(t.Kinds) != 0 {
		return fmt.Errorf("kind and kinds cannot both be set")
	}
	if t.Kind == "" && len(t.Kinds) == 0 && t.Name == "" && t.Namespace == "" && t.LabelSelector == nil && t.AnnotationSelector == nil {
		return fmt.Errorf("empty selector")
	}
	for _, kind := range append([]string{t.Kind}, t.Kinds...) {
		if kind != "" && !supportedKind[kind] {
			return fmt.Errorf("unsupported kind %q", kind)
		}
	}
	if t.LabelSelector != nil {
		if len(t.LabelSelector.MatchLabels) == 0 && len(t.LabelSelector.MatchExpressions) == 0 {
			return fmt.Errorf("empty labelSelector")
		}
		if _, err := labelSelector(t.LabelSelector); err != nil {
			return err
		}
	}
	if t.AnnotationSelector != nil && len(t.AnnotationSelector.MatchAnnotations) == 0 {
		return fmt.Errorf("empty annotationSelector")
	}
	if exclusion {
		if len(t.Inject) != 0 {
			return fmt.Errorf("exclude cannot contain inject")
		}
		return nil
	}
	if t.Name == "" && t.LabelSelector == nil && t.AnnotationSelector == nil {
		return fmt.Errorf("target requires name, labelSelector, or annotationSelector")
	}
	if len(t.Inject) == 0 {
		return fmt.Errorf("inject is required")
	}
	settings, err := normalize(t.Inject)
	if err != nil {
		return err
	}
	return validateSettings(settings)
}

func normalize(input map[string]any) (map[string]string, error) {
	out := map[string]string{}
	set := func(key, value string) error {
		key = strings.TrimPrefix(key, prefix)
		if key == "vault-namespace" {
			key = "namespace"
		}
		if old, exists := out[key]; exists && old != value {
			return fmt.Errorf("conflicting inject setting %q", key)
		}
		out[key] = value
		return nil
	}
	for key, value := range input {
		if key == "annotations" {
			m, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("inject.annotations must be a map")
			}
			for k, v := range m {
				s, ok := v.(string)
				if !ok {
					return nil, fmt.Errorf("annotation %q must be a string", k)
				}
				if err := set(k, s); err != nil {
					return nil, err
				}
			}
			continue
		}
		s, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("inject.%s must be a string", key)
		}
		if err := set(key, s); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func validateSettings(s map[string]string) error { return validateSettingsFor(s, true) }

func validateSettingsFor(s map[string]string, allowOfficial bool) error {
	var unsupported []string
	for key, val := range s {
		switch {
		case key == "role", key == "vault-addr", key == "service", key == "auth-path", key == "agent-image", key == "namespace", key == "vault-namespace", key == "token-audience", key == "mount-path", key == "volume-name", key == "agent-inject-containers", key == "tls-secret", key == "tls-ca-cert":
			if val == "" {
				return fmt.Errorf("%s cannot be empty", key)
			}
		case key == "agent-pre-populate", key == "agent-pre-populate-only", key == "agent-init-first", key == "agent-inject", key == "allow-http":
			if val != "true" && val != "false" {
				return fmt.Errorf("%s must be true or false", key)
			}
		case key == "agent-inject-default-template":
			if val != "map" && val != "json" {
				return fmt.Errorf("agent-inject-default-template must be map or json")
			}
		case key == "agent-requests-cpu", key == "agent-limits-cpu", key == "agent-requests-mem", key == "agent-limits-mem", key == "agent-requests-ephemeral", key == "agent-limits-ephemeral":
			if val != "" {
				q, err := resource.ParseQuantity(val)
				if err != nil || q.Sign() <= 0 {
					return fmt.Errorf("%s must be a positive Kubernetes quantity", key)
				}
			}
		case key == "template-static-secret-render-interval":
			d, err := time.ParseDuration(val)
			if err != nil || d <= 0 {
				return fmt.Errorf("%s must be a positive duration", key)
			}
		case key == "token-expiration":
			n, err := strconv.Atoi(val)
			if err != nil || n < 600 || n > 86400 {
				return fmt.Errorf("token-expiration must be 600..86400 seconds")
			}
		case key == "agent-run-as-user", key == "agent-run-as-group":
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 || n > 65535 {
				return fmt.Errorf("%s must be a non-root ID from 1..65535", key)
			}
		case strings.HasPrefix(key, "agent-inject-secret-"), strings.HasPrefix(key, "agent-inject-template-"), strings.HasPrefix(key, "agent-inject-file-"), strings.HasPrefix(key, "agent-inject-perms-"):
			if val == "" {
				return fmt.Errorf("%s cannot be empty", key)
			}
		default:
			if !allowOfficial || !isOfficialAnnotation(key) {
				unsupported = append(unsupported, key)
			}
		}
	}
	if len(unsupported) > 0 {
		sort.Strings(unsupported)
		return fmt.Errorf("unsupported settings: %s", strings.Join(unsupported, ", "))
	}
	if (s["tls-secret"] == "") != (s["tls-ca-cert"] == "") {
		return fmt.Errorf("tls-secret and tls-ca-cert must be supplied together")
	}
	if s["namespace"] != "" && s["vault-namespace"] != "" && s["namespace"] != s["vault-namespace"] {
		return fmt.Errorf("namespace and vault-namespace conflict")
	}
	return nil
}

func labelSelector(s *Selector) (labels.Selector, error) {
	l := &metav1.LabelSelector{MatchLabels: s.MatchLabels}
	for _, e := range s.MatchExpressions {
		l.MatchExpressions = append(l.MatchExpressions, metav1.LabelSelectorRequirement{Key: e.Key, Operator: metav1.LabelSelectorOperator(e.Operator), Values: e.Values})
	}
	sel, err := metav1.LabelSelectorAsSelector(l)
	if err != nil {
		return nil, fmt.Errorf("invalid labelSelector: %w", err)
	}
	return sel, nil
}

func matches(t Target, resource map[string]any, namespace string) (bool, error) {
	kind, _ := resource["kind"].(string)
	if t.Kind != "" && t.Kind != kind {
		return false, nil
	}
	if len(t.Kinds) > 0 {
		found := false
		for _, k := range t.Kinds {
			if k == kind {
				found = true
			}
		}
		if !found {
			return false, nil
		}
	}
	meta := object(resource, "metadata")
	if t.Name != "" && t.Name != str(meta, "name") {
		return false, nil
	}
	if t.Namespace != "" && t.Namespace != namespace {
		return false, nil
	}
	if t.LabelSelector != nil {
		sel, err := labelSelector(t.LabelSelector)
		if err != nil {
			return false, err
		}
		if !sel.Matches(labels.Set(stringMap(object(meta, "labels")))) {
			return false, nil
		}
	}
	if t.AnnotationSelector != nil {
		anns := object(meta, "annotations")
		for k, v := range t.AnnotationSelector.MatchAnnotations {
			if str(anns, k) != v {
				return false, nil
			}
		}
	}
	return true, nil
}

func targetSpecificity(t Target) int {
	score := 0
	if t.Name != "" {
		score += 8
	}
	if t.Namespace != "" {
		score += 4
	}
	if t.Kind != "" {
		score += 2
	}
	if t.LabelSelector != nil {
		score++
	}
	if t.AnnotationSelector != nil {
		score++
	}
	return score
}

func stringMap(m map[string]any) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}
func object(m map[string]any, key string) map[string]any { v, _ := m[key].(map[string]any); return v }
func ensureObject(m map[string]any, key string) map[string]any {
	if v := object(m, key); v != nil {
		return v
	}
	v := map[string]any{}
	m[key] = v
	return v
}
func str(m map[string]any, key string) string  { s, _ := m[key].(string); return s }
func array(m map[string]any, key string) []any { v, _ := m[key].([]any); return v }

func podParts(resource map[string]any) (map[string]any, map[string]any, error) {
	var base map[string]any
	switch str(resource, "kind") {
	case "Pod":
		base = resource
	case "CronJob":
		base = object(object(object(resource, "spec"), "jobTemplate"), "spec")
		base = object(base, "template")
	default:
		base = object(object(resource, "spec"), "template")
	}
	if base == nil || object(base, "spec") == nil {
		return nil, nil, fmt.Errorf("%s %q has no PodSpec", str(resource, "kind"), str(object(resource, "metadata"), "name"))
	}
	return ensureObject(base, "metadata"), object(base, "spec"), nil
}

func Render(input []byte, cfg Config, releaseNamespace string) ([]byte, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(input))
	var docs []map[string]any
	for {
		var node yaml.Node
		err := decoder.Decode(&node)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("manifest YAML: %w", err)
		}
		if err := deduplicateManifestKeys(&node); err != nil {
			return nil, fmt.Errorf("manifest YAML: %w", err)
		}
		var doc map[string]any
		if err := node.Decode(&doc); err != nil {
			return nil, fmt.Errorf("manifest YAML: %w", err)
		}
		if len(doc) > 0 {
			docs = append(docs, doc)
		}
	}
	if !cfg.Injector.Enabled || (len(cfg.Injector.Targets) == 0 && !cfg.Injector.AnnotationDiscovery && cfg.Injector.Mode != "all") {
		return encode(docs)
	}
	configMaps := map[string]map[string]any{}
	for _, doc := range docs {
		if str(doc, "kind") == "ConfigMap" {
			m := object(doc, "metadata")
			ns := str(m, "namespace")
			if ns == "" {
				ns = releaseNamespace
			}
			configMaps[ns+"/"+str(m, "name")] = doc
		}
	}
	var generated []map[string]any
	for _, doc := range docs {
		kind := str(doc, "kind")
		if !supportedKind[kind] {
			continue
		}
		meta := object(doc, "metadata")
		if meta == nil || str(meta, "name") == "" {
			return nil, fmt.Errorf("%s missing metadata.name", kind)
		}
		namespace := str(meta, "namespace")
		if namespace == "" {
			namespace = releaseNamespace
		}
		selected := map[string]string{}
		priority := map[string]int{}
		matched := cfg.Injector.Mode == "all" && namespace != "kube-system" && namespace != "kube-public" && namespace != "kube-node-lease"
		if cfg.Injector.AnnotationDiscovery {
			podMeta, _, err := podParts(doc)
			if err == nil {
				raw := str(object(podMeta, "annotations"), prefix+"agent-inject")
				if raw != "" {
					enabled, err := parseutil.ParseBool(raw)
					if err != nil {
						return nil, fmt.Errorf("%s %s: invalid agent-inject annotation: %w", kind, str(meta, "name"), err)
					}
					matched = matched || enabled
				}
			}
		}
		disabled := false
		for _, target := range cfg.Injector.Targets {
			ok, err := matches(target, doc, namespace)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			settings, err := normalize(target.Inject)
			if err != nil {
				return nil, err
			}
			if settings["agent-inject"] == "false" {
				disabled = true
			}
			score := targetSpecificity(target)
			for k, v := range settings {
				if old, exists := selected[k]; exists {
					if priority[k] == score && old != v {
						return nil, fmt.Errorf("%s %s: conflicting equally specific target setting %s", kind, str(meta, "name"), k)
					}
					if priority[k] > score {
						continue
					}
				}
				selected[k] = v
				priority[k] = score
			}
			matched = true
		}
		if !matched || disabled {
			continue
		}
		excluded := false
		for _, t := range cfg.Injector.Exclude {
			ok, err := matches(t, doc, namespace)
			if err != nil {
				return nil, err
			}
			if ok {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		podMeta, podSpec, err := podParts(doc)
		if err != nil {
			return nil, err
		}
		podAnns := ensureObject(podMeta, "annotations")
		nativeRaw := str(podAnns, prefix+"agent-inject")
		nativeEnabled := false
		if nativeRaw != "" {
			nativeEnabled, err = parseutil.ParseBool(nativeRaw)
			if err != nil {
				return nil, fmt.Errorf("%s %s: invalid agent-inject annotation: %w", kind, str(meta, "name"), err)
			}
			if !nativeEnabled && selected["agent-inject"] != "true" {
				continue
			}
		}
		if str(podAnns, marker) == "true" {
			cmName := name(cfg.Injector.NamePrefix, str(meta, "name"), kind)
			cm := configMaps[namespace+"/"+cmName]
			if cm == nil || str(object(object(cm, "metadata"), "labels"), marker) != "true" {
				return nil, fmt.Errorf("%s %s: renderer marker without generated ConfigMap", kind, str(meta, "name"))
			}
			if err := verifyInjected(podSpec, podAnns, cm); err != nil {
				return nil, fmt.Errorf("%s %s: %w", kind, str(meta, "name"), err)
			}
			continue
		}
		if nativeEnabled || nativeRaw != "" && selected["agent-inject"] == "true" {
			settings := map[string]string{}
			if err := mergeOfficialLayer(settings, cfg.Injector.Defaults); err != nil {
				return nil, fmt.Errorf("%s %s: %w", kind, str(meta, "name"), err)
			}
			native := map[string]string{}
			for key, value := range podAnns {
				if text, ok := value.(string); ok && strings.HasPrefix(key, prefix) {
					native[strings.TrimPrefix(key, prefix)] = text
				}
			}
			if err := mergeOfficialLayer(settings, native); err != nil {
				return nil, fmt.Errorf("%s %s: %w", kind, str(meta, "name"), err)
			}
			if err := mergeOfficialLayer(settings, selected); err != nil {
				return nil, fmt.Errorf("%s %s: %w", kind, str(meta, "name"), err)
			}
			if settings["agent-inject"] != "false" {
				if err := injectOfficial(doc, podMeta, podSpec, namespace, settings); err != nil {
					return nil, fmt.Errorf("%s %s: %w", kind, str(meta, "name"), err)
				}
			}
			continue
		}
		settings := map[string]string{"auth-path": "auth/kubernetes", "agent-image": "hashicorp/vault:1.20.4", "agent-pre-populate": "true", "agent-pre-populate-only": "false", "mount-path": "/vault/secrets", "volume-name": "vault-secrets", "token-audience": "vault", "token-expiration": "3600", "agent-requests-cpu": "250m", "agent-limits-cpu": "500m", "agent-requests-mem": "64Mi", "agent-limits-mem": "128Mi"}
		for k, v := range cfg.Injector.Defaults {
			if k == "vault-namespace" {
				k = "namespace"
			}
			settings[k] = v
		}
		for k, v := range podAnns {
			if s, ok := v.(string); ok && strings.HasPrefix(k, prefix) {
				settings[strings.TrimPrefix(k, prefix)] = s
			}
		}
		for k, v := range selected {
			settings[k] = v
		}
		if settings["agent-inject"] == "false" {
			continue
		}
		if err := validateSettingsFor(settings, false); err != nil {
			return nil, fmt.Errorf("%s %s: %w", kind, str(meta, "name"), err)
		}
		cm, err := inject(doc, podMeta, podSpec, settings, cfg, namespace)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", kind, str(meta, "name"), err)
		}
		cmMeta := object(cm, "metadata")
		key := namespace + "/" + str(cmMeta, "name")
		if configMaps[key] != nil {
			return nil, fmt.Errorf("generated ConfigMap %s collides with existing resource", key)
		}
		configMaps[key] = cm
		generated = append(generated, cm)
	}
	docs = append(generated, docs...)
	return encode(docs)
}

func deduplicateManifestKeys(node *yaml.Node) error {
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]int)
		content := make([]*yaml.Node, 0, len(node.Content))
		for i := 0; i < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if key.Kind == yaml.ScalarNode {
				if previous, exists := seen[key.Value]; exists {
					if strings.HasPrefix(key.Value, prefix) {
						return fmt.Errorf("duplicate Vault annotation %q at lines %d and %d", key.Value, content[previous].Line, key.Line)
					}
					content[previous+1] = value
					continue
				}
				seen[key.Value] = len(content)
			}
			content = append(content, key, value)
		}
		node.Content = content
	}
	for _, child := range node.Content {
		if err := deduplicateManifestKeys(child); err != nil {
			return err
		}
	}
	return nil
}

func encode(docs []map[string]any) ([]byte, error) {
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	for _, doc := range docs {
		if err := enc.Encode(doc); err != nil {
			return nil, err
		}
	}
	return out.Bytes(), nil
}

func verifyInjected(spec, annotations, cm map[string]any) error {
	data := object(cm, "data")
	initHCL, initOK := data["agent-init.hcl"].(string)
	sideHCL, sideOK := data["agent.hcl"].(string)
	if !initOK || !sideOK {
		return fmt.Errorf("generated ConfigMap lacks Agent configuration")
	}
	hash := sha256.Sum256([]byte(initHCL + "\x00" + sideHCL))
	if str(annotations, checksumKey) != hex.EncodeToString(hash[:]) {
		return fmt.Errorf("generated ConfigMap checksum mismatch")
	}
	configVolume := false
	for _, v := range array(spec, "volumes") {
		if m, ok := v.(map[string]any); ok && str(m, "name") == "vault-agent-config" && str(object(m, "configMap"), "name") == str(object(cm, "metadata"), "name") {
			configVolume = true
		}
	}
	agent := false
	for _, key := range []string{"containers", "initContainers"} {
		for _, v := range array(spec, key) {
			if m, ok := v.(map[string]any); ok && (str(m, "name") == "vault-agent" || str(m, "name") == "vault-agent-init") {
				agent = true
			}
		}
	}
	if !configVolume || !agent {
		return fmt.Errorf("renderer marker has missing injection artifacts")
	}
	return nil
}

func inject(resource, podMeta, podSpec map[string]any, s map[string]string, cfg Config, namespace string) (map[string]any, error) {
	if s["role"] == "" {
		return nil, fmt.Errorf("role is required")
	}
	addr := s["vault-addr"]
	if s["service"] != "" {
		addr = s["service"]
	}
	if addr == "" {
		return nil, fmt.Errorf("Vault address is required: set vault.hashicorp.com/service or an optional renderer vault-addr default")
	}
	u, err := url.Parse(addr)
	if err != nil || (u.Scheme != "https" && !(u.Scheme == "http" && s["allow-http"] == "true")) || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("https vault-addr is required unless allow-http is explicitly true")
	}
	if !strings.HasPrefix(s["auth-path"], "auth/") || strings.Contains(s["auth-path"], "..") {
		return nil, fmt.Errorf("invalid auth-path")
	}
	mount := s["mount-path"]
	if !path.IsAbs(mount) || path.Clean(mount) != mount || mount == "/" {
		return nil, fmt.Errorf("invalid mount-path")
	}
	volume := s["volume-name"]
	if !dnsName.MatchString(volume) || len(volume) > 63 {
		return nil, fmt.Errorf("invalid volume-name")
	}
	if s["tls-ca-cert"] != "" && (path.Base(s["tls-ca-cert"]) != s["tls-ca-cert"] || s["tls-ca-cert"] == "..") {
		return nil, fmt.Errorf("invalid tls-ca-cert")
	}
	user, group := 1000, 1000
	if s["agent-run-as-user"] != "" {
		user = mustInt(s["agent-run-as-user"])
	}
	if s["agent-run-as-group"] != "" {
		group = mustInt(s["agent-run-as-group"])
	}
	podSecurity := ensureObject(podSpec, "securityContext")
	if existing, ok := podSecurity["fsGroup"].(int); ok {
		if s["agent-run-as-group"] != "" && existing != group {
			return nil, fmt.Errorf("agent-run-as-group conflicts with Pod fsGroup")
		}
		group = existing
	} else {
		podSecurity["fsGroup"] = group
	}
	secretPaths := map[string]string{}
	for k, v := range s {
		if strings.HasPrefix(k, "agent-inject-secret-") {
			secretPaths[strings.TrimPrefix(k, "agent-inject-secret-")] = v
		}
	}
	if len(secretPaths) == 0 {
		return nil, fmt.Errorf("at least one agent-inject-secret-* setting is required")
	}
	ids := make([]string, 0, len(secretPaths))
	for id := range secretPaths {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	destinations := map[string]bool{}
	for _, id := range ids {
		filename := s["agent-inject-file-"+id]
		if filename == "" {
			filename = id
		}
		if path.IsAbs(filename) || filename == "." || filename == ".." || path.Base(filename) != filename || strings.Contains(filename, "\\") {
			return nil, fmt.Errorf("unsafe file destination for %s", id)
		}
		if destinations[filename] {
			return nil, fmt.Errorf("duplicate template destination %s", filename)
		}
		destinations[filename] = true
		if p := s["agent-inject-perms-"+id]; p != "" {
			if len(p) != 4 || strings.Trim(p, "01234567") != "" {
				return nil, fmt.Errorf("invalid permissions for %s", id)
			}
		}
	}
	for k := range s {
		for _, stem := range []string{"agent-inject-file-", "agent-inject-template-", "agent-inject-perms-"} {
			if strings.HasPrefix(k, stem) && secretPaths[strings.TrimPrefix(k, stem)] == "" {
				return nil, fmt.Errorf("%s has no matching secret", k)
			}
		}
	}
	configName := name(cfg.Injector.NamePrefix, str(object(resource, "metadata"), "name"), str(resource, "kind"))
	identity := []string{"vault-agent-init", "vault-agent"}
	for _, key := range []string{"containers", "initContainers"} {
		for _, v := range array(podSpec, key) {
			if m, ok := v.(map[string]any); ok {
				for _, n := range identity {
					if str(m, "name") == n {
						return nil, fmt.Errorf("container name %s already exists", n)
					}
				}
			}
		}
	}
	for _, v := range array(podSpec, "volumes") {
		if m, ok := v.(map[string]any); ok {
			for _, n := range []string{volume, "vault-agent-config", "vault-agent-token", "vault-agent-tls"} {
				if str(m, "name") == n {
					return nil, fmt.Errorf("volume name %s already exists", n)
				}
			}
		}
	}
	selected := map[string]bool{}
	if s["agent-inject-containers"] != "" {
		for _, n := range strings.Split(s["agent-inject-containers"], ",") {
			selected[strings.TrimSpace(n)] = true
		}
	}
	containers := array(podSpec, "containers")
	if len(containers) == 0 {
		return nil, fmt.Errorf("PodSpec has no containers")
	}
	for _, v := range containers {
		container, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid container")
		}
		if len(selected) > 0 && !selected[str(container, "name")] {
			continue
		}
		for _, mv := range array(container, "volumeMounts") {
			if m, ok := mv.(map[string]any); ok && (str(m, "name") == volume || str(m, "mountPath") == mount) {
				return nil, fmt.Errorf("mount collision in container %s", str(container, "name"))
			}
		}
		container["volumeMounts"] = append(array(container, "volumeMounts"), map[string]any{"name": volume, "mountPath": mount, "readOnly": true})
		delete(selected, str(container, "name"))
	}
	if len(selected) > 0 {
		return nil, fmt.Errorf("agent-inject-containers names unknown container")
	}
	initHCL := agentHCL(s, ids, secretPaths, true)
	sideHCL := agentHCL(s, ids, secretPaths, false)
	data := map[string]any{"agent-init.hcl": initHCL, "agent.hcl": sideHCL}
	hash := sha256.Sum256([]byte(initHCL + "\x00" + sideHCL))
	sum := hex.EncodeToString(hash[:])
	cmMeta := map[string]any{"name": configName, "labels": map[string]any{marker: "true"}}
	if namespace != "" {
		cmMeta["namespace"] = namespace
	}
	for k, v := range cfg.Injector.Labels {
		cmMeta["labels"].(map[string]any)[k] = v
	}
	cm := map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": cmMeta, "data": data}
	volumes := array(podSpec, "volumes")
	volumes = append(volumes, map[string]any{"name": volume, "emptyDir": map[string]any{"medium": "Memory"}}, map[string]any{"name": "vault-agent-config", "configMap": map[string]any{"name": configName}}, map[string]any{"name": "vault-agent-token", "projected": map[string]any{"sources": []any{map[string]any{"serviceAccountToken": map[string]any{"path": "token", "audience": s["token-audience"], "expirationSeconds": mustInt(s["token-expiration"])}}}}})
	if s["tls-secret"] != "" {
		volumes = append(volumes, map[string]any{"name": "vault-agent-tls", "secret": map[string]any{"secretName": s["tls-secret"]}})
	}
	podSpec["volumes"] = volumes
	mounts := []any{map[string]any{"name": volume, "mountPath": mount}, map[string]any{"name": "vault-agent-config", "mountPath": "/vault/config", "readOnly": true}, map[string]any{"name": "vault-agent-token", "mountPath": "/vault/token", "readOnly": true}}
	if s["tls-secret"] != "" {
		mounts = append(mounts, map[string]any{"name": "vault-agent-tls", "mountPath": "/vault/tls", "readOnly": true})
	}
	requests := map[string]any{}
	limits := map[string]any{}
	for _, entry := range []struct {
		key, name string
		target    map[string]any
	}{{"agent-requests-cpu", "cpu", requests}, {"agent-requests-mem", "memory", requests}, {"agent-requests-ephemeral", "ephemeral-storage", requests}, {"agent-limits-cpu", "cpu", limits}, {"agent-limits-mem", "memory", limits}, {"agent-limits-ephemeral", "ephemeral-storage", limits}} {
		if v := s[entry.key]; v != "" {
			entry.target[entry.name] = v
		}
	}
	resources := map[string]any{"requests": requests, "limits": limits}
	base := func(filename, n string) map[string]any {
		return map[string]any{"name": n, "image": s["agent-image"], "command": []any{"/bin/vault"}, "args": []any{"agent", "-config=/vault/config/" + filename}, "volumeMounts": mounts, "resources": resources, "securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "runAsNonRoot": true, "runAsUser": user, "runAsGroup": group, "capabilities": map[string]any{"drop": []any{"ALL"}}}}
	}
	pre := s["agent-pre-populate"] == "true"
	only := s["agent-pre-populate-only"] == "true"
	if only && !pre {
		return nil, fmt.Errorf("agent-pre-populate-only requires agent-pre-populate=true")
	}
	if pre {
		initAgent := base("agent-init.hcl", "vault-agent-init")
		if s["agent-init-first"] == "true" {
			podSpec["initContainers"] = append([]any{initAgent}, array(podSpec, "initContainers")...)
		} else {
			podSpec["initContainers"] = append(array(podSpec, "initContainers"), initAgent)
		}
	}
	if !only {
		podSpec["containers"] = append(containers, base("agent.hcl", "vault-agent"))
	}
	anns := ensureObject(podMeta, "annotations")
	for k := range anns {
		if strings.HasPrefix(k, prefix) {
			delete(anns, k)
		}
	}
	anns[marker] = "true"
	anns[checksumKey] = sum
	anns[prefix+"agent-inject-status"] = "injected"
	return cm, nil
}

func mustInt(s string) int { n, _ := strconv.Atoi(s); return n }

func name(custom, resource, kind string) string {
	base := resource + "-" + strings.ToLower(kind) + "-vault-agent"
	if custom != "" {
		base = custom + "-" + base
	}
	base = invalidDNS.ReplaceAllString(base, "-")
	if len(base) <= 63 && dnsName.MatchString(base) {
		return base
	}
	h := sha256.Sum256([]byte(kind + "/" + resource))
	suffix := "-" + hex.EncodeToString(h[:])[:10]
	base = strings.Trim(base[:min(len(base), 63-len(suffix))], "-")
	return base + suffix
}

func agentHCL(s map[string]string, ids []string, secrets map[string]string, init bool) string {
	f := hclwrite.NewEmptyFile()
	root := f.Body()
	root.SetAttributeValue("exit_after_auth", cty.BoolVal(init))
	vault := root.AppendNewBlock("vault", nil).Body()
	addr := s["vault-addr"]
	if s["service"] != "" {
		addr = s["service"]
	}
	vault.SetAttributeValue("address", cty.StringVal(addr))
	if s["tls-secret"] != "" {
		vault.SetAttributeValue("ca_cert", cty.StringVal("/vault/tls/"+s["tls-ca-cert"]))
	}
	auth := root.AppendNewBlock("auto_auth", nil).Body()
	method := auth.AppendNewBlock("method", []string{"kubernetes"}).Body()
	method.SetAttributeValue("mount_path", cty.StringVal(s["auth-path"]))
	if s["namespace"] != "" {
		method.SetAttributeValue("namespace", cty.StringVal(s["namespace"]))
	}
	method.SetAttributeValue("config", cty.ObjectVal(map[string]cty.Value{"role": cty.StringVal(s["role"]), "token_path": cty.StringVal("/vault/token/token")}))
	if interval := s["template-static-secret-render-interval"]; interval != "" {
		root.AppendNewBlock("template_config", nil).Body().SetAttributeValue("static_secret_render_interval", cty.StringVal(interval))
	}
	for _, id := range ids {
		filename := s["agent-inject-file-"+id]
		if filename == "" {
			filename = id
		}
		content := s["agent-inject-template-"+id]
		if content == "" {
			if s["agent-inject-default-template"] == "json" {
				content = "{{- with secret " + strconv.Quote(secrets[id]) + " -}}\n{{ .Data | toJSON }}\n{{- end }}\n"
			} else {
				content = "{{- with secret " + strconv.Quote(secrets[id]) + " -}}\n{{- range $key, $value := .Data -}}\n{{ $key }}: {{ $value }}\n{{- end -}}\n{{- end -}}\n"
			}
		}
		block := root.AppendNewBlock("template", nil).Body()
		block.SetAttributeValue("contents", cty.StringVal(content))
		block.SetAttributeValue("destination", cty.StringVal(path.Join(s["mount-path"], filename)))
		perms := s["agent-inject-perms-"+id]
		if perms == "" {
			perms = "0640"
		}
		block.SetAttributeValue("perms", cty.StringVal(perms))
	}
	return string(f.Bytes())
}
