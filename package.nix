{ lib, buildGoModule }:

buildGoModule (finalAttrs: {
  pname = "gerardo";
  version = "0.1.0";

  src = lib.fileset.toSource {
    root = ./.;
    fileset = lib.fileset.unions [
      ./go.mod
      ./go.sum
      ./main.go
      ./internal
    ];
  };

  vendorHash = "sha256-A3yvAAGermm4vdR7R3dkBqb0eCLrYflypv1Ab8bXM/s=";

  ldflags = [
    "-s"
    "-w"
    "-X main.version=v${finalAttrs.version}"
  ];

  # The integration tests serve a fake Gerrit on the loopback interface.
  __darwinAllowLocalNetworking = true;

  meta = {
    description = "Terminal client for reviewing Gerrit changes";
    homepage = "https://github.com/peano88/gerardo";
    license = lib.licenses.mit;
    mainProgram = "gerardo";
  };
})
