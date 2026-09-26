_ktop_namespaces() {
    kubectl get ns -o name 2>/dev/null | sed 's|^namespace/||'
}

_ktop_contexts() {
    kubectl config get-contexts -o name 2>/dev/null
}

_ktop() {
    local cur prev opts
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    opts="-h --help -V --version -i --interval -n --namespace -c --context"
    local scopes="po d rs ds sts panic status"

    case "$prev" in
        -i|--interval)
            return 0
            ;;
        -n|--namespace)
            COMPREPLY=( $(compgen -W "$(_ktop_namespaces)" -- "$cur") )
            return 0
            ;;
        -c|--context)
            COMPREPLY=( $(compgen -W "$(_ktop_contexts)" -- "$cur") )
            return 0
            ;;
    esac

    case "$cur" in
        -*)
            COMPREPLY=( $(compgen -W "$opts" -- "$cur") )
            ;;
        *)
            COMPREPLY=( $(compgen -W "$scopes $(_ktop_namespaces)" -- "$cur") )
            ;;
    esac
}

complete -F _ktop ktop
