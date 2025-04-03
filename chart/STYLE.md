# Helm chart code style

Due to the complicated reusability characteristics of the podseidon helm chart,
the code style used in the templates is different from the typical charts,
and adds some rules to avoid common footguns.

> The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL
> NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED",  "MAY", and
> "OPTIONAL" in this document are to be interpreted as described in
> RFC 2119.


1. All templates MUST be defined under `templates/_{xxx}.yaml`.
  The component files `template/{component}.yaml` SHOULD only contain direct invocation of the boilerplate entrypoint.
2. Template naming rules:
  1. All template names MUST be suffixed with the data type provided by the template:
    - `.json` for templates that provide a single compact JSON object
    - `.json-array` for templates that provide a single JSON array
    - `.yaml` for templates that provide a single compact YAML object
    - `.yaml-array` for templates that provide a single YAML array
    - `.obj` for templates that emit one or multiple K8S objects with leading `---`
    - `.string` for templates that contain a string seagment that can be interpolated into another string
3. Use of `{{}}` padding:
  1. Flow control directives (i.e. everything at the same level as `{{end}}`)
    MUST always start with `{{- ` and end with `}}`.
  2. Included JSON objects and strings MUST start with `{{` and end with `}}`.
  3. Non-indented YAML interpolation MUST start with `{{` and end with `}}`.
  4. YAML content MUST NOT be interpolated.
    The content should be provided in a separate template at root indent level instead,
    and the referrer may include it with `include ... | fromYaml | toJson`.
  5. Templates of `.string` type MUST surround any literal content with `{{- }}`.
5. All variables, when used as a YAML value, MUST be piped through a `toJson`.
6. All interpolated strings MUST be piped through `printf | toJson`.
7. All templates SHOULD expect a dictionary parameter with the keys listed below.
  Templates that accept other keys MUST be explicitly documented.
  - `.main`: main helm context
  - `.component`: current component name, must be one of "generator", "aggergator" or "webhook"
  - `.instance`: suffix of the deployment,
    typically same as `.component` but MAY be extended to allow multiple distinct deployments of the same component;
    if there are multiple instances, EXACTLY ONE instance MUST be equal to `.component` to generate shared objects
  - `.generic`: equivalent to `(get .main.Values .component)`
8. All arguments passed to `merge` MUST be `deepCopy`ed,
  unless the value originates from a `fromYaml`/`fromYamlArray` directly
  and is not used anywhere else.
