function __ktop_namespaces
    kubectl get ns -o name 2>/dev/null | string replace 'namespace/' ''
end

complete -c ktop -f
complete -c ktop -s h -l help -d "show help"
complete -c ktop -s V -l version -d "show version"
complete -c ktop -s i -l interval -x -d "refresh interval in seconds"
complete -c ktop -s c -l context -x -d "kube context" \
    -a "(kubectl config get-contexts -o name 2>/dev/null)"
complete -c ktop -s n -l namespace -x -d namespace -a "(__ktop_namespaces)"
complete -c ktop -n __fish_is_first_arg -a "(__ktop_namespaces)" -d namespace
