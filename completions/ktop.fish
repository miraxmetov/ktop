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
complete -c ktop -n __fish_is_first_arg -a po -d "open on pods"
complete -c ktop -n __fish_is_first_arg -a d -d "open on deployments"
complete -c ktop -n __fish_is_first_arg -a rs -d "open on replica sets"
complete -c ktop -n __fish_is_first_arg -a ds -d "open on daemon sets"
complete -c ktop -n __fish_is_first_arg -a sts -d "open on stateful sets"
complete -c ktop -n __fish_is_first_arg -a panic -d "open on critical deployments"
complete -c ktop -n __fish_is_first_arg -a status -d "print the status line and exit"
