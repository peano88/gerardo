{
  description = "gerardo, a terminal client for reviewing Gerrit changes";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:
    {
      overlays.default = final: _prev: {
        gerardo = final.callPackage ./package.nix { };
      };
    }
    // flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        gerardo = pkgs.callPackage ./package.nix { };
      in
      {
        packages = {
          inherit gerardo;
          default = gerardo;
        };

        apps.default = flake-utils.lib.mkApp { drv = gerardo; };

        devShells.default = pkgs.mkShell {
          inputsFrom = [ gerardo ];
          packages = [ pkgs.gopls ];
        };
      }
    );
}
