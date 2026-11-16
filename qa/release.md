# Release builds

| ID | Mode | Scenario | Expected |
|---|---|---|---|
| REL-1 | CI | Open a pull request | The `build` workflow is green: `go vet` passes and the four targets compile |
| REL-2 | CI | Push a `vX.Y.Z` tag | A GitHub release named after the tag holds `ps5cast-windows-amd64.exe` and the three `.tar.gz` archives |
| REL-3 | Local | Run the build step with `GITHUB_REF_NAME=v9.9.9`, start the binary | The banner shows `ps5cast 9.9.9` |
| REL-4 | Local, macOS | Start the darwin binary without `sudo`, query `dig @127.0.0.1 example.com` and `http://localhost:8080` | DNS answers and the page returns 200 |
| REL-5 | Manual, Linux | Extract the archive, run the `setcap` command from the README, start `./ps5cast` | DNS listens on port 53 without `sudo` |
| REL-6 | Manual, Windows | Download the `.exe` from the latest release and start it | The banner is printed and the web page opens |
