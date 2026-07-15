package backendmigration

import (
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/workspaceidentity"
)

const safeConfigurationText = "backend provider configuration"

// ValueOrigin identifies where a prospective target value came from.
type ValueOrigin uint8

const (
	ValueOriginUnknown ValueOrigin = iota
	ValueOriginExplicitFlag
	ValueOriginEnvironment
	ValueOriginWorkspace
	ValueOriginOther
)

// PostgreSQLTargetRequest contains prospective, not-yet-authoritative target
// identity values.
type PostgreSQLTargetRequest struct {
	Locator       string
	Schema        string
	LocatorOrigin ValueOrigin
	SchemaOrigin  ValueOrigin
}

// ProviderConfigurationRequest combines explicit target identity with the
// source-selection request that must be witnessed.
type ProviderConfigurationRequest struct {
	Selection SelectionRequest
	Target    PostgreSQLTargetRequest
}

// EmbeddedDoltReadOnlyConfiguration is an inert source identity snapshot.
type EmbeddedDoltReadOnlyConfiguration struct {
	state *embeddedDoltReadOnlyConfigurationState
}

type embeddedDoltReadOnlyConfigurationState struct {
	beadsDir string
	database string
	branch   string
}

func (c EmbeddedDoltReadOnlyConfiguration) BeadsDir() string {
	if c.state == nil {
		return ""
	}
	return c.state.beadsDir
}
func (c EmbeddedDoltReadOnlyConfiguration) Database() string {
	if c.state == nil {
		return ""
	}
	return c.state.database
}
func (c EmbeddedDoltReadOnlyConfiguration) Branch() string {
	if c.state == nil {
		return ""
	}
	return c.state.branch
}
func (EmbeddedDoltReadOnlyConfiguration) String() string   { return safeConfigurationText }
func (EmbeddedDoltReadOnlyConfiguration) GoString() string { return safeConfigurationText }

// PostgreSQLTargetConfiguration is an inert target identity snapshot.
type PostgreSQLTargetConfiguration struct {
	state *postgreSQLTargetConfigurationState
}

type postgreSQLTargetConfigurationState struct {
	baseDSN string
	schema  string
}

// BaseDSN is an identity serialization, not provider-open authority.
func (c PostgreSQLTargetConfiguration) BaseDSN() string {
	if c.state == nil {
		return ""
	}
	return c.state.baseDSN
}
func (c PostgreSQLTargetConfiguration) Schema() string {
	if c.state == nil {
		return ""
	}
	return c.state.schema
}
func (PostgreSQLTargetConfiguration) String() string   { return safeConfigurationText }
func (PostgreSQLTargetConfiguration) GoString() string { return safeConfigurationText }

// BoundProviderConfiguration is an inert copy of revalidated source and target
// identity values.
type BoundProviderConfiguration struct {
	state *boundProviderConfigurationState
}

type boundProviderConfigurationState struct {
	source EmbeddedDoltReadOnlyConfiguration
	target PostgreSQLTargetConfiguration
}

func (c BoundProviderConfiguration) Source() EmbeddedDoltReadOnlyConfiguration {
	if c.state == nil {
		return EmbeddedDoltReadOnlyConfiguration{}
	}
	return c.state.source
}
func (c BoundProviderConfiguration) Target() PostgreSQLTargetConfiguration {
	if c.state == nil {
		return PostgreSQLTargetConfiguration{}
	}
	return c.state.target
}
func (BoundProviderConfiguration) String() string   { return safeConfigurationText }
func (BoundProviderConfiguration) GoString() string { return safeConfigurationText }

// ProviderConfigurationBinding owns configuration identity and its source
// witness until Close. Callers use it only through pointer lifecycle methods;
// the shared private state also keeps incidental formatting copies inert.
type ProviderConfigurationBinding struct {
	state *providerConfigurationBindingState
}

type providerConfigurationBindingState struct {
	mu         sync.Mutex
	closed     bool
	closeErr   error
	witness    sourceWitness
	shape      shapeObservation
	filesystem workspaceidentity.FilesystemSnapshot
	observe    func(string) (shapeObservation, error)
	inspectFS  func(sourceWitness) (workspaceidentity.FilesystemSnapshot, error)
	equalFS    func(workspaceidentity.FilesystemSnapshot, workspaceidentity.FilesystemSnapshot) bool
	config     BoundProviderConfiguration
}

func (ProviderConfigurationBinding) String() string   { return safeConfigurationText }
func (ProviderConfigurationBinding) GoString() string { return safeConfigurationText }

// BindProviderConfiguration binds a migration provider configuration.
func BindProviderConfiguration(request ProviderConfigurationRequest) (*ProviderConfigurationBinding, error) {
	return bindProviderConfigurationWith(request, productionSelectionDependencies())
}

func bindProviderConfigurationWith(request ProviderConfigurationRequest, deps selectionDependencies) (*ProviderConfigurationBinding, error) {
	if request.Selection.TargetBackend != configfile.BackendPostgres {
		return nil, refusal(CodePairUnsupported, ReasonTargetBackend, false, nil)
	}
	target, err := admitPostgreSQLTarget(request.Target)
	if err != nil {
		return nil, err
	}
	var admitted *retainedSourceAdmission
	if _, err := inspectSourceShapeRetainedWith(request.Selection, deps, &admitted); err != nil {
		return nil, err
	}
	if admitted == nil || admitted.witness == nil {
		return nil, refusal(CodeWorkspaceUnverifiable, ReasonRequest, false, nil)
	}
	database := admitted.database
	if database == "" {
		database = configfile.DefaultDoltDatabase
	}
	if !validTargetIdentifier(database, false) {
		primary := refusal(CodeWorkspaceShapeUnsupported, ReasonMetadataValues, false, nil)
		if closeErr := admitted.witness.Close(); closeErr != nil {
			return nil, cleanupRefusal(primary, closeErr)
		}
		return nil, primary
	}

	return &ProviderConfigurationBinding{state: &providerConfigurationBindingState{
		witness:    admitted.witness,
		shape:      admitted.shape,
		filesystem: admitted.filesystem,
		observe:    admitted.observe,
		inspectFS:  admitted.inspectFS,
		equalFS:    admitted.equalFS,
		config: BoundProviderConfiguration{state: &boundProviderConfigurationState{
			source: EmbeddedDoltReadOnlyConfiguration{state: &embeddedDoltReadOnlyConfigurationState{
				beadsDir: admitted.workspace,
				database: database,
				branch:   "main",
			}},
			target: target,
		}}}}, nil
}

func admitPostgreSQLTarget(request PostgreSQLTargetRequest) (PostgreSQLTargetConfiguration, error) {
	if request.LocatorOrigin != ValueOriginExplicitFlag {
		return PostgreSQLTargetConfiguration{}, refusal(CodePairUnsupported, ReasonTargetLocatorSource, false, nil)
	}
	if request.SchemaOrigin != ValueOriginExplicitFlag {
		return PostgreSQLTargetConfiguration{}, refusal(CodePairUnsupported, ReasonTargetSchemaSource, false, nil)
	}
	if !safeLocatorText(request.Locator) {
		return PostgreSQLTargetConfiguration{}, refusal(CodePairUnsupported, ReasonTargetLocator, false, nil)
	}

	parsed, parseErr := url.Parse(request.Locator)
	if parseErr != nil {
		return PostgreSQLTargetConfiguration{}, refusal(CodePairUnsupported, ReasonTargetLocator, false, nil)
	}
	if targetContainsCredential(parsed) {
		return PostgreSQLTargetConfiguration{}, refusal(CodeCredentialInLocator, ReasonTargetCredential, false, nil)
	}
	if !strings.HasPrefix(request.Locator, "postgres://") && !strings.HasPrefix(request.Locator, "postgresql://") {
		return PostgreSQLTargetConfiguration{}, refusal(CodePairUnsupported, ReasonTargetLocator, false, nil)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" || parsed.Opaque != "" ||
		strings.Contains(request.Locator, "#") || parsed.Fragment != "" || parsed.RawFragment != "" {
		return PostgreSQLTargetConfiguration{}, refusal(CodePairUnsupported, ReasonTargetLocator, false, nil)
	}
	if parsed.User == nil || !validTargetIdentifier(parsed.User.Username(), true) {
		return PostgreSQLTargetConfiguration{}, refusal(CodePairUnsupported, ReasonTargetLocator, false, nil)
	}
	database, ok := targetDatabase(parsed)
	if !ok {
		return PostgreSQLTargetConfiguration{}, refusal(CodePairUnsupported, ReasonTargetLocator, false, nil)
	}
	host, port, ok := canonicalTargetEndpoint(parsed)
	if !ok {
		return PostgreSQLTargetConfiguration{}, refusal(CodePairUnsupported, ReasonTargetTransport, false, nil)
	}
	mode, ok := targetSSLMode(parsed)
	if !ok {
		return PostgreSQLTargetConfiguration{}, refusal(CodePairUnsupported, ReasonTargetOptions, false, nil)
	}
	if !validTargetSchema(request.Schema) {
		return PostgreSQLTargetConfiguration{}, refusal(CodePairUnsupported, ReasonTargetSchema, false, nil)
	}

	identity := (&url.URL{
		Scheme:   "postgresql",
		User:     url.User(parsed.User.Username()),
		Host:     net.JoinHostPort(host, port),
		Path:     "/" + database,
		RawQuery: "sslmode=" + mode,
	}).String()
	return PostgreSQLTargetConfiguration{state: &postgreSQLTargetConfigurationState{
		baseDSN: identity,
		schema:  request.Schema,
	}}, nil
}

func safeLocatorText(locator string) bool {
	if len(locator) == 0 || len(locator) > 4096 || !utf8.ValidString(locator) {
		return false
	}
	for _, value := range locator {
		if unicode.IsControl(value) {
			return false
		}
	}
	return true
}

func targetContainsCredential(parsed *url.URL) bool {
	if parsed.User != nil {
		if _, present := parsed.User.Password(); present {
			return true
		}
	}
	for _, pair := range strings.FieldsFunc(parsed.RawQuery, func(value rune) bool {
		return value == '&' || value == ';'
	}) {
		rawKey, _, _ := strings.Cut(pair, "=")
		key, err := url.QueryUnescape(rawKey)
		if err == nil && (strings.EqualFold(key, "password") || strings.EqualFold(key, "sslpassword")) {
			return true
		}
	}
	return false
}

func targetDatabase(parsed *url.URL) (string, bool) {
	if !strings.HasPrefix(parsed.Path, "/") {
		return "", false
	}
	database := strings.TrimPrefix(parsed.Path, "/")
	if strings.Contains(database, "/") || !validTargetIdentifier(database, true) {
		return "", false
	}
	return database, true
}

func canonicalTargetEndpoint(parsed *url.URL) (string, string, bool) {
	rawAuthority := parsed.Host
	if rawAuthority == "" || strings.Contains(rawAuthority, "%") {
		return "", "", false
	}

	bracketed := strings.HasPrefix(rawAuthority, "[")
	host := parsed.Hostname()
	if host == "" || strings.Contains(host, "%") || hasUnsafeHostCharacter(host) {
		return "", "", false
	}
	if bracketed {
		closing := strings.LastIndexByte(rawAuthority, ']')
		if closing < 0 || !strings.Contains(host, ":") {
			return "", "", false
		}
		suffix := rawAuthority[closing+1:]
		if suffix != "" && (!strings.HasPrefix(suffix, ":") || len(suffix) == 1 || strings.Contains(suffix[1:], ":")) {
			return "", "", false
		}
	} else {
		colonCount := strings.Count(rawAuthority, ":")
		if colonCount > 1 || colonCount == 1 && strings.HasSuffix(rawAuthority, ":") {
			return "", "", false
		}
	}

	canonicalHost, ok := canonicalTargetHost(host)
	if !ok {
		return "", "", false
	}
	rawPort := parsed.Port()
	if rawPort == "" {
		rawPort = "5432"
	}
	for i := range len(rawPort) {
		if rawPort[i] < '0' || rawPort[i] > '9' {
			return "", "", false
		}
	}
	port, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil || port == 0 {
		return "", "", false
	}
	return canonicalHost, strconv.FormatUint(port, 10), true
}

func hasUnsafeHostCharacter(host string) bool {
	for _, value := range host {
		if value > unicode.MaxASCII || unicode.IsControl(value) || unicode.IsSpace(value) ||
			value == ',' || value == '/' || value == '\\' {
			return true
		}
	}
	return false
}

func canonicalTargetHost(host string) (string, bool) {
	if address := net.ParseIP(host); address != nil {
		if ipv4 := address.To4(); ipv4 != nil {
			return ipv4.String(), true
		}
		return address.String(), true
	}
	if strings.Contains(host, ":") || strings.HasPrefix(strings.ToLower(host), "0x") || onlyDigitsAndDots(host) ||
		len(host) > 253 || strings.HasSuffix(host, ".") {
		return "", false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || !asciiLetterOrDigit(label[0]) || !asciiLetterOrDigit(label[len(label)-1]) {
			return "", false
		}
		for i := range len(label) {
			if !asciiLetterOrDigit(label[i]) && label[i] != '-' {
				return "", false
			}
		}
	}
	return strings.ToLower(host), true
}

func onlyDigitsAndDots(value string) bool {
	if value == "" {
		return false
	}
	for i := range len(value) {
		if (value[i] < '0' || value[i] > '9') && value[i] != '.' {
			return false
		}
	}
	return true
}

func targetSSLMode(parsed *url.URL) (string, bool) {
	const require = "sslmode=require"
	const verifyFull = "sslmode=verify-full"
	switch parsed.RawQuery {
	case require:
		return "require", !parsed.ForceQuery
	case verifyFull:
		return "verify-full", !parsed.ForceQuery
	default:
		return "", false
	}
}

func validTargetIdentifier(value string, allowDotHyphen bool) bool {
	if len(value) == 0 || len(value) > 63 || !utf8.ValidString(value) || !asciiLetterOrUnderscore(value[0]) {
		return false
	}
	for i := 1; i < len(value); i++ {
		if asciiLetterOrDigit(value[i]) || value[i] == '_' || allowDotHyphen && (value[i] == '.' || value[i] == '-') {
			continue
		}
		return false
	}
	return true
}

func validTargetSchema(schema string) bool {
	if len(schema) == 0 || len(schema) > 63 || (schema[0] < 'a' || schema[0] > 'z') && schema[0] != '_' {
		return false
	}
	for i := 1; i < len(schema); i++ {
		if (schema[i] < 'a' || schema[i] > 'z') && (schema[i] < '0' || schema[i] > '9') && schema[i] != '_' {
			return false
		}
	}
	return schema != "public" && schema != "information_schema" && !strings.HasPrefix(schema, "pg_")
}

func asciiLetterOrUnderscore(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func asciiLetterOrDigit(value byte) bool {
	return asciiLetterOrUnderscore(value) && value != '_' || value >= '0' && value <= '9'
}

// Snapshot returns a copy of the currently bound configuration.
func (b *ProviderConfigurationBinding) Snapshot() (BoundProviderConfiguration, error) {
	if b == nil || b.state == nil {
		return BoundProviderConfiguration{}, closedBindingRefusal()
	}
	state := b.state
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed || state.witness == nil || state.observe == nil || state.inspectFS == nil || state.equalFS == nil {
		return BoundProviderConfiguration{}, closedBindingRefusal()
	}

	if err := state.witness.Revalidate(); err != nil {
		return BoundProviderConfiguration{}, classifyWitnessError(err)
	}
	if err := state.observeAndCompare(); err != nil {
		return BoundProviderConfiguration{}, err
	}
	if err := state.inspectFilesystemAndCompare(); err != nil {
		return BoundProviderConfiguration{}, err
	}
	if err := state.observeAndCompare(); err != nil {
		return BoundProviderConfiguration{}, err
	}
	if err := state.inspectFilesystemAndCompare(); err != nil {
		return BoundProviderConfiguration{}, err
	}
	return state.config, nil
}

func (s *providerConfigurationBindingState) observeAndCompare() error {
	observed, err := s.observe(s.config.Source().BeadsDir())
	if err != nil {
		return classifyObservationError(err)
	}
	if err := s.witness.Revalidate(); err != nil {
		return classifyWitnessError(err)
	}
	if !s.shape.Equal(observed) {
		return changedRefusal(workspaceidentity.ErrChanged)
	}
	return nil
}

func (s *providerConfigurationBindingState) inspectFilesystemAndCompare() error {
	observed, err := s.inspectFS(s.witness)
	if err != nil {
		return classifyFilesystemError(err)
	}
	if err := s.witness.Revalidate(); err != nil {
		return classifyWitnessError(err)
	}
	if !s.equalFS(s.filesystem, observed) {
		return changedRefusal(workspaceidentity.ErrChanged)
	}
	return nil
}

func closedBindingRefusal() error {
	return refusal(CodeWorkspaceUnverifiable, ReasonBindingClosed, false, workspaceidentity.ErrClosed)
}

// Close releases the retained witness exactly once and stores its sanitized
// result for every later call.
func (b *ProviderConfigurationBinding) Close() error {
	if b == nil || b.state == nil {
		return nil
	}
	state := b.state
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return state.closeErr
	}
	state.closed = true
	witness := state.witness
	state.witness = nil
	state.shape = shapeObservation{}
	state.filesystem = workspaceidentity.FilesystemSnapshot{}
	state.observe = nil
	state.inspectFS = nil
	state.equalFS = nil
	state.config = BoundProviderConfiguration{}
	if witness != nil {
		if err := witness.Close(); err != nil {
			state.closeErr = cleanupRefusal(nil, err)
		}
	}
	return state.closeErr
}
