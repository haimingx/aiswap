package main

import (
	"fmt"
	"os"
)

func runCompletion(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: aiswap completion <zsh|bash|fish>")
		return 1
	}
	switch args[0] {
	case "zsh":
		fmt.Print(`#compdef aiswap
_aiswap() {
  local -a commands providers
  commands=(
    'tui:Open interactive dashboard'
    'watch:Open live quota monitor'
    'status:Show live accounts and quotas'
    'list:List managed slots'
    'models:List live available models for active accounts'
    'add:Save current login into a slot'
    'switch:Switch active account (slot, alias, email, next, best)'
    'remove:Remove a saved slot'
    'alias:Set or unset a slot alias'
    'disable:Hold a slot out of rotation'
    'enable:Return a slot to rotation'
    'stats:Show offline token usage stats'
    'doctor:Check local environment and providers'
    'upgrade:Self-upgrade aiswap from GitHub Releases'
    'update:Alias for upgrade'
    'completion:Generate shell completion script'
    'version:Print version'
  )
  providers=(claude codex agy)
  if (( CURRENT == 2 )); then
    _describe 'command' commands
  elif (( CURRENT == 3 )); then
    case "$words[2]" in
      status|list|ls|models|add|switch|sw|remove|rm|alias|disable|enable)
        _describe 'provider' providers ;;
      completion)
        compadd zsh bash fish ;;
    esac
  fi
}
compdef _aiswap aiswap
`)
		return 0
	case "bash":
		fmt.Print(`_aiswap_completions() {
  local cur prev commands providers
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev="${COMP_WORDS[COMP_CWORD-1]}"
  commands="tui watch status list models add switch remove alias disable enable stats doctor upgrade update completion version"
  providers="claude codex agy"
  if [[ ${COMP_CWORD} -eq 1 ]]; then
    COMPREPLY=( $(compgen -W "${commands}" -- "${cur}") )
  elif [[ ${COMP_CWORD} -eq 2 ]]; then
    case "${prev}" in
      status|list|ls|models|add|switch|sw|remove|rm|alias|disable|enable)
        COMPREPLY=( $(compgen -W "${providers}" -- "${cur}") ) ;;
      completion)
        COMPREPLY=( $(compgen -W "zsh bash fish" -- "${cur}") ) ;;
    esac
  fi
}
complete -F _aiswap_completions aiswap
`)
		return 0
	case "fish":
		fmt.Print(`complete -c aiswap -f
complete -c aiswap -n '__fish_use_subcommand' -a 'tui watch status list models add switch remove alias disable enable stats doctor upgrade update completion version'
complete -c aiswap -n '__fish_seen_subcommand_from status list ls models add switch sw remove rm alias disable enable' -a 'claude codex agy'
complete -c aiswap -n '__fish_seen_subcommand_from completion' -a 'zsh bash fish'
`)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unsupported shell %q (expected: zsh, bash, fish)\n", args[0])
		return 1
	}
}
