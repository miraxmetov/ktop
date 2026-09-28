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
complete -c ktop -n __fish_is_first_arg -a no -d "open on nodes"
complete -c ktop -n __fish_is_first_arg -a ns -d "open on namespaces"
complete -c ktop -n __fish_is_first_arg -a quota -d "open on resource quotas"
complete -c ktop -n __fish_is_first_arg -a limits -d "open on limit ranges"
complete -c ktop -n __fish_is_first_arg -a svc -d "open on services"
complete -c ktop -n __fish_is_first_arg -a eps -d "open on endpoint slices"
complete -c ktop -n __fish_is_first_arg -a ing -d "open on ingresses"
complete -c ktop -n __fish_is_first_arg -a netpol -d "open on network policies"
complete -c ktop -n __fish_is_first_arg -a gw -d "open on gateways"
complete -c ktop -n __fish_is_first_arg -a hr -d "open on HTTP routes"
complete -c ktop -n __fish_is_first_arg -a pvc -d "open on volume claims"
complete -c ktop -n __fish_is_first_arg -a pv -d "open on volumes"
complete -c ktop -n __fish_is_first_arg -a sc -d "open on storage classes"
complete -c ktop -n __fish_is_first_arg -a panic -d "open on critical deployments"
complete -c ktop -n __fish_is_first_arg -a status -d "print the status line and exit"
