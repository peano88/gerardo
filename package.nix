{ lib, buildGoModule }:

buildGoModule {
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

  vendorHash = "sha256-Y2rSFE0Niwbf6q5UsWIpLptl+ZJHwumlDWtVYf8gFDE=";

  ldflags = [
    "-s"
    "-w"
  ];

  # The integration tests serve a fake Gerrit on the loopback interface.
  __darwinAllowLocalNetworking = true;

  meta = {
    description = "Terminal client for reviewing Gerrit changes";
    mainProgram = "gerardo";
  };
}
