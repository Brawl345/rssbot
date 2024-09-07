{
  description = "RSS bot for Telegram";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixpkgs-unstable";
  };

  outputs = { self, nixpkgs, ... }:

    let
      forAllSystems = function:
        nixpkgs.lib.genAttrs [
          "x86_64-linux"
          "aarch64-linux"
          "x86_64-darwin"
          "aarch64-darwin"
        ]
          (system: function nixpkgs.legacyPackages.${system});

      version =
        if (self ? shortRev)
        then self.shortRev
        else "dev";
    in
    {

      nixosModules = {
        default = ./module.nix;
      };

      overlays.default = final: prev: {
        rssbot = self.packages.${prev.system}.default;
      };

      devShells = forAllSystems
        (pkgs: {
          default = pkgs.mkShell {
            packages = [
              pkgs.go
              pkgs.golangci-lint
            ];
          };
        });


      packages = forAllSystems
        (pkgs: {
          gobot =
            pkgs.buildGoModule
              {
                pname = "rssbot";
                inherit version;
                src = pkgs.lib.cleanSource self;

                # Update the hash if go dependencies change!
                # vendorHash = pkgs.lib.fakeHash;
                vendorHash = "sha256-mo30V7ISVFY8Rl3yXChP6pbehV9hTPH3UlBLDb1dzNE=";

                ldflags = [ "-s" "-w" ];

                meta = {
                  description = "RSS bot for Telegram";
                  homepage = "https://github.com/Brawl345/rssbot";
                  license = pkgs.lib.licenses.unlicense;
                  platforms = pkgs.lib.platforms.darwin ++ pkgs.lib.platforms.linux;
                };
              };

          default = self.packages.${pkgs.system}.gobot;
        });
    };
}
