SHELLSPEC:=shellspec
ENTR:=entr
SH:=sh
BASH:=bash
KSH:=ksh
SHELLS:=$(SH) $(BASH) $(KSH)
DATE:=date

.PHONY: test
test:
	$(SHELLSPEC)

.PHONY: ci
ci:
	find . -name "*.sh" -type f | $(ENTR) -d sh -ec "for s in $(SHELLS); do $(SHELLSPEC) --shell \$$s; done; $(DATE)"
