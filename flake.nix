{
  description = "onWatch - Go CLI for AI quota tracking";

  inputs = {
    # nixos-25.05 ships Go 1.24 (and go_1_25 = 1.25.5); go.mod requires
    # >= 1.25.7, so we track nixos-unstable which defaults to Go 1.26.x.
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        lib = pkgs.lib;
        version = builtins.readFile ./VERSION;
        onwatch = pkgs.buildGoModule {
          pname = "onwatch";
          inherit version;
          src = ./.;
          subPackages = [ "." ];
          vendorHash = "sha256-zagPclPZItTTUaMh+8Ph7k5ESqc3vETPNkhMQ493MoY=";
          ldflags = [
            "-s"
            "-w"
            "-X main.version=${version}"
          ];
          env.CGO_ENABLED = 0;
        };
      in
      {
        packages = {
          inherit onwatch;
          default = onwatch;
        };

        apps.default = {
          type = "app";
          program = "${onwatch}/bin/onwatch";
        };

        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            gotools
            gofumpt
          ];
        };
      });
}
