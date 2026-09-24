# Starts the bot with the NixOS module and a local MariaDB. Without network the
# bot cannot reach Telegram, but it connects to the database and applies the
# migrations first, which covers the module, the hardening and socket auth.
self:
{ pkgs, ... }:
{
  name = "rssbot";

  nodes.machine = {
    imports = [ self.nixosModules.default ];

    services.rssbot = {
      enable = true;
      adminId = 1337;
      botTokenFile = pkgs.writeText "rssbot-token" "123456789:test";
      template = "<b>{{.Title}}</b>";
      poll = {
        interval = "5m";
        adaptive = false;
      };
    };
  };

  testScript = ''
    machine.wait_for_unit("mysql.service")
    machine.wait_until_succeeds("journalctl -u rssbot.service | grep -q 'Applied 6 migration'")
    machine.succeed("mysql -N rssbot -e 'SELECT COUNT(*) FROM replacements' | grep -qx 41")
    machine.succeed("systemctl show rssbot.service -p Environment | grep -q POLL_INTERVAL=5m")
    print(machine.succeed("systemd-analyze security rssbot.service | tail -1"))
  '';
}
