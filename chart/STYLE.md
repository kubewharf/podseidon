# Helm chart code style

Due to the complicated reusability characteristics of the podseidon helm chart,
the code style used in the templates is different from the typical charts,
and adds some rules to avoid common footguns.

> The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL
> NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED",  "MAY", and
> "OPTIONAL" in this document are to be interpreted as described in
> RFC 2119.


- All templates MUST be defined under `templates/_{xxx}.yaml`.
  The component files `template/{component}.yaml` SHOULD only contain direct invocation of the boilerplate entrypoint.
- Template naming rules:
  - All template names MUST be suffixed with the data type provided by the template:
    - `.json` for templates that provide a single compact JSON object
    - `.json-array` for templates that provide a single JSON array
    - `.yaml` for templates that provide a single compact YAML object
    - `.yaml-array` for templates that provide a single YAML array
    - `.obj` for templates that emit one or multiple K8S objects with leading `---`
    - `.string` for templates that contain a string segment that can be interpolated into another string
- Use of `{{}}` padding:
  - Flow control directives (i.e. everything at the same level as `{{end}}`)
    MUST always start with `{{- ` and end with `}}`.
  - Included JSON objects and strings MUST start with `{{` and end with `}}`.
  - Non-indented YAML interpolation MUST start with `{{` and end with `}}`.
  - YAML content MUST NOT be interpolated.
    The content should be provided in a separate template at root indent level instead,
    and the referrer may include it with `include ... | fromYaml | toJson`.
  - Templates of `.string` type MUST surround any literal content with `{{- }}`.
- All variables, when used as a YAML value, MUST be piped through a `toJson`.
- All interpolated strings MUST be piped through `printf | toJson`.
- All templates SHOULD expect a dictionary parameter with the keys listed below.
  Templates that accept other keys MUST be explicitly documented.
  - `.main`: main helm context
  - `.component`: current component name
  - `.generic`: equivalent to `(get .main.Values .component)`
- All arguments passed to `merge` MUST be `deepCopy`ed,
  unless the value originates from a `fromYaml`/`fromYamlArray` directly
  and is not used anywhere else.
