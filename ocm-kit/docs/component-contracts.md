# Component contracts

The `contract` package implements [RFC01](rfcs/rfc01-ocm-component-contracts.md):
a component-local YAML resource declares what a component **requires** from its
environment and what it **provides**. Label the resource with
`ext.ocm.software/component-contract`, value `v1`. The schema is
`v1`, kind `ComponentContract`.

The first iteration matches `requires.apis` and `requires.features` against other
components' `provides` declarations. It works entirely with component content;
it does not connect to Kubernetes or require a kubeconfig.

The fixtures include an [application](../test/fixtures/contracts/application),
[cert-manager](../test/fixtures/contracts/cert-manager), and a
[database provider](../test/fixtures/contracts/database-provider). The application
requires certificate APIs and a controller from cert-manager, plus a PostgreSQL
API and capability from the database provider.

Build a separate local CTF for each component from the repository root:

```bash
for fixture in application cert-manager database-provider; do
  (cd "test/fixtures/contracts/$fixture" && \
    ocm add component-version --repository ./ctf --constructor component-constructor.yaml)
done

ocm-kit contract show test/fixtures/contracts/application/ctf

# Three certificate requirements are satisfied; two database requirements are missing.
ocm-kit contract verify test/fixtures/contracts/application/ctf \
  --against test/fixtures/contracts/cert-manager/ctf

# All five requirements are satisfied across the two provider components.
ocm-kit contract verify test/fixtures/contracts/application/ctf \
  --against test/fixtures/contracts/cert-manager/ctf \
  --against test/fixtures/contracts/database-provider/ctf
```

The commands also accept the same OCI and explicit CTF references as Helm
rendering. Both commands default to readable text; `-o yaml` (or `--output yaml`)
prints the contract or evaluation report as YAML. The existing `ocm-kit <ref>`
rendering command is preserved. `resolve` is an alias for `verify`.

The reports show which provider satisfies each requirement. Try leaving out either
provider or editing the database API's version and rebuilding its CTF to see the
matching behavior. These fixtures are declarations only; they do not install
cert-manager, PostgreSQL, or the application.

For library callers, `compver.Open(ctx, ref)` loads the repository and descriptor
for both Helm values and contracts; close the returned component when finished.
`GetContract(ctx, repo, desc)` discovers and parses the resource.
`NewCatalogProbe([]Provider{...})` validates the providers and returns a probe
or an error. Pass the probe to `Verify(ctx, c, probes...)`, which also accepts custom
`Probe` implementations. It returns
`Satisfied` (with its source), `Missing`, `Unverified`, and `Errors`.

Schema validation and requirement evaluation are separate. `Parse` and
`ComponentContract.Validate` check the declaration's fields, including Kubernetes
version constraints, node counts, CPU and memory quantities, device counts, and
label syntax. They do not check the version or capacity of an actual cluster.

Contract types use `json` tags for both JSON and YAML serialization;
`sigs.k8s.io/yaml.UnmarshalStrict` rejects unknown and duplicate fields. `Parse`
also rejects multiple YAML documents before calling `ComponentContract.Validate`.
`Verify` and `NewCatalogProbe` also call `Validate` for contracts created in Go.

Field rules live in `validate` tags on the Go contract types and are evaluated by
`go-playground/validator/v10`. Custom rules check nonblank strings, semver
constraints, positive Kubernetes quantities, and Kubernetes label syntax. Optional
sections are checked when present; list entries are checked with `dive`. Validation
errors include YAML field paths and report all invalid fields (after checking the
API version). Empty API groups and label values remain valid, and a zero node
count retains its default of one during evaluation.

Catalog resolution checks provider **claims**, without installing providers or
evaluating their own requirements. API requirements match group, kind, and at least
one listed version; feature requirements match the capability identifier exactly.
Hints and endpoints are descriptive metadata. Requirements under `kubernetes`,
`cluster`, and `nodes` remain `Unverified` by the catalog probe. Live-cluster
verification is deferred to a later iteration. Each component version is evaluated
independently.

Commands exit `0` after printing a contract or evaluation report. Missing and
unverified requirements and probe errors are findings in the report; they do not
change the exit code. Invalid arguments, malformed contracts, missing contract
resources, and loading or output failures return an error and exit `1`.

Run `task test` for parsing, contract discovery from local CTFs, probe
orchestration, catalog matching, and CLI tests. Contract tests do not require
Docker or a registry.
