# Homebrew formula template for ESFS. Update url/sha256 for a tagged release.
# Note: a FUSE backend is required at runtime and is intentionally NOT a hard
# dependency, so users can choose macFUSE (kext) or FUSE-T (kextless).
class Esfs < Formula
  desc "Elasticsearch as an SMFS-style filesystem (mount, routed ls/find/grep)"
  homepage "https://github.com/dbmurphy/elasticsearch-filesystem"
  version "0.1.0"
  url "https://github.com/dbmurphy/elasticsearch-filesystem/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "REPLACE_WITH_RELEASE_SHA256"
  license "MIT"

  depends_on "go" => :build

  def install
    cmds = %w[esfs esfsd esfs-grep esfs-ls esfs-find]
    ldflags = "-s -w -X github.com/dbmurphy/elasticsearch-filesystem/internal/cli.Version=#{version}"
    cmds.each do |c|
      system "go", "build", *std_go_args(ldflags: ldflags, output: bin/c), "./cmd/#{c}"
    end
  end

  def caveats
    <<~EOS
      ESFS needs a FUSE backend at runtime:
        macOS:  FUSE-T (kextless, preferred) or macFUSE
        Linux:  fuse3 / fusermount3

      Activate routed ls/find/grep in your shell:
        eval "$(esfs env)"

      Verify your setup:
        esfs doctor
    EOS
  end

  test do
    assert_match "esfs", shell_output("#{bin}/esfs version")
  end
end
