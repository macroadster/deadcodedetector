package java

import (
	"path/filepath"
	"strings"
)

// Resolver maps Java type names to files on disk under the scan root.
type Resolver struct {
	root     string
	files    map[string]string            // slash-rel -> abs
	byFQCN   map[string]string            // com.example.Foo -> abs
	byPkg    map[string]map[string]string // package -> simple name -> abs
	pkgFiles map[string][]string          // package -> abs files
}

func newResolver(root string, units map[string]*unit) *Resolver {
	r := &Resolver{
		root:     root,
		files:    map[string]string{},
		byFQCN:   map[string]string{},
		byPkg:    map[string]map[string]string{},
		pkgFiles: map[string][]string{},
	}
	for abs, u := range units {
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			continue
		}
		slash := filepath.ToSlash(rel)
		r.files[slash] = abs
		pkg := u.Package
		r.pkgFiles[pkg] = append(r.pkgFiles[pkg], abs)
		names := r.byPkg[pkg]
		if names == nil {
			names = map[string]string{}
			r.byPkg[pkg] = names
		}
		for _, td := range u.Types {
			fqcn := typeFQCN(pkg, td, u.Types)
			if fqcn != "" {
				r.byFQCN[fqcn] = abs
			}
			if !td.Nested {
				names[td.Name] = abs
			}
		}
		// Fallback: file name matches public type when the parser missed it.
		base := strings.TrimSuffix(filepath.Base(abs), ".java")
		if base != "" && names[base] == "" {
			names[base] = abs
			if pkg == "" {
				if r.byFQCN[base] == "" {
					r.byFQCN[base] = abs
				}
			} else if r.byFQCN[pkg+"."+base] == "" {
				r.byFQCN[pkg+"."+base] = abs
			}
		}
	}
	return r
}

// ResolveFQCN maps a fully-qualified type name to a local file.
func (r *Resolver) ResolveFQCN(qual string) string {
	if qual == "" {
		return ""
	}
	if abs := r.byFQCN[qual]; abs != "" {
		return abs
	}
	// Static import: com.example.Foo.member → com.example.Foo
	if i := strings.LastIndex(qual, "."); i > 0 {
		if abs := r.byFQCN[qual[:i]]; abs != "" {
			return abs
		}
	}
	return ""
}

// ResolveSimple maps a simple type name in pkg (same-package lookup).
func (r *Resolver) ResolveSimple(pkg, name string) string {
	if name == "" {
		return ""
	}
	if names := r.byPkg[pkg]; names != nil {
		return names[name]
	}
	return ""
}

// PackageFiles returns every compilation unit in pkg.
func (r *Resolver) PackageFiles(pkg string) []string {
	return r.pkgFiles[pkg]
}

func isSpecialFile(rel string) bool {
	base := filepath.Base(rel)
	return base == "package-info.java" || base == "module-info.java"
}

func isTestFile(rel string) bool {
	rel = filepath.ToSlash(rel)
	base := filepath.Base(rel)
	if strings.HasSuffix(base, "Test.java") || strings.HasSuffix(base, "Tests.java") ||
		strings.HasSuffix(base, "IT.java") || strings.HasSuffix(base, "ITCase.java") ||
		strings.HasSuffix(base, "TestCase.java") {
		return true
	}
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		if p == "test" || p == "tests" {
			return true
		}
		// Maven/Gradle: src/test/java
		if p == "src" && i+1 < len(parts) && parts[i+1] == "test" {
			return true
		}
	}
	return false
}

func isEntryName(base string) bool {
	switch base {
	case "Main.java", "Application.java", "App.java":
		return true
	}
	return false
}

// Framework annotations that make a type an entry (reflection / DI / servlet).
func isEntryAnnotation(name string) bool {
	switch name {
	case "SpringBootApplication", "SpringBootConfiguration",
		"RestController", "Controller", "ControllerAdvice", "RestControllerAdvice",
		"Service", "Component", "Repository", "Configuration", "ConfigurationProperties",
		"Entity", "MappedSuperclass", "Embeddable",
		"WebServlet", "WebFilter", "WebListener", "MessageDriven",
		"Application", "QuarkusMain", "ApplicationScoped", "RequestScoped",
		"Path", "Provider", // JAX-RS
		"EntityType", "Converter":
		return true
	}
	return false
}

func isEntryMethodAnnotation(name string) bool {
	switch name {
	case "Test", "ParameterizedTest", "RepeatedTest", "TestFactory", "TestTemplate",
		"BeforeEach", "AfterEach", "BeforeAll", "AfterAll", "Before", "After",
		"Scheduled", "EventListener", "KafkaListener", "RabbitListener", "JmsListener",
		"PostConstruct", "PreDestroy", "Bean",
		"GetMapping", "PostMapping", "PutMapping", "DeleteMapping", "PatchMapping",
		"RequestMapping", "ExceptionHandler", "MessageMapping",
		"Get", "Post", "Put", "Delete", "Patch": // JAX-RS
		return true
	}
	return false
}

func typeFQCN(pkg string, td TypeDecl, all []TypeDecl) string {
	parts := []string{td.Name}
	enc := td.Enclosing
	guard := 0
	for enc != "" && guard < 8 {
		guard++
		parts = append([]string{enc}, parts...)
		next := ""
		for _, t := range all {
			if t.Name == enc {
				next = t.Enclosing
				break
			}
		}
		enc = next
	}
	if pkg == "" {
		return strings.Join(parts, ".")
	}
	return pkg + "." + strings.Join(parts, ".")
}

func isLifecycleName(name string) bool {
	switch name {
	case "main", "hashCode", "equals", "toString", "compareTo", "clone",
		"finalize", "readObject", "writeObject", "readResolve", "writeReplace",
		"serialVersionUID", "values", "valueOf", "builder", "build", "of",
		"from", "copy", "copyOf", "newBuilder",
		// Hadoop Record / WritableComparable generated helpers.
		"signature", "slurpRaw", "compareRaw":
		return true
	}
	return false
}

func isBeanAccessor(name string) bool {
	if len(name) < 3 {
		return false
	}
	if strings.HasPrefix(name, "get") || strings.HasPrefix(name, "set") {
		r := name[3]
		return r >= 'A' && r <= 'Z'
	}
	if strings.HasPrefix(name, "is") && len(name) > 2 {
		r := name[2]
		return r >= 'A' && r <= 'Z'
	}
	return false
}
