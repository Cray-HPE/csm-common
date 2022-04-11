# Common shell related functions/libraries

Ensure that all scripts/libraries pass shellcheck cleanly (ignoring zsh as its not supported by shellcheck). If you must disable a check note why that check cannot be satisfied and constrain it to the single line at issue.

Libraries should conform to posix shell so that in the event they run on a /bin/sh that is not bash, e.g. dash or busybox sh, everything works fine.

## Layout

All libraries are in the [lib](lib) directory.

All tests/specs are in the [spec](spec) directory.

## Testing

All tests are built with [shellspec](https://github.com/shellspec/shellspec). New functions should have a spec describing default and non default behavior.

You will need to install shellspec somewhere to test, as long as the `shellspec` command is in $PATH you should be fine.

## Development/testing

There is a (GNUmakefile)[GNUmakefile] with a few targets provided to make development a bit easier.

`make test` gives you a quick way to just run shellcheck basically

`make ci` gives you a way to have all the shellspec tests run across `sh` `bash` `ksh` using the [entr](https://github.com/eradman/entr) utility.

If you're missing any of those shells just use the *SHELLS* variable to change what gets tested with something like so (note this also means you can test any random binary as if it was a shell):

`make ci SHELLS="/bin/sh /bin/bash /bin/ksh"`

Refer to the entr and/or shellspec pages on how to install those utilities for your platform of choice.

## Note for zsh

Note on `zsh`, as the libraries here are posix compliant, and likely use posix word split behavior you will need to use emulate when sourcing the libraries:

```
source_sh() {
  emulate sh
  builtin source "$@"
}

source_sh ./lib/logger.sh
```

Or you can follow the [zsh faq guidance](https://zsh.sourceforge.io/FAQ/zshfaq03.html). Zsh isn't tested in this setup due to there being no easy way to patch in the above into shellspec from what I could find. Everything should work, just make sure zsh behaves more like posix sh when sourcing.
