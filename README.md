# HeroOnPrem

## Status

In **Development**, do not use or at your own risks...

## About

This library triggers a slurm job based on input file and a *.hero* matching file.

The goal is to use this library in a file upload hook mechanism to trigger a job on uploaded files, a *serverless* data service.

*.hero* file can be located in user home directory or a subdirectory of the file.
If multiple files exist, the nearer of input file will be used.

Example:

    hero:  # map of experiment and job definition
        exp1:
            methods: []  # [optional] list of methods to apply rules [add, delete, edit]
            rules:  # list of path (regexp) to match against input file to run a script
                - /home  # example all files under /home
            cpus: 3  # [optional] number of cpus
            mem: 10  # [optional] optional memory requirements in G
            time: 05:00:00  # [optional] slurm time
            queue: long  # [optional] slurm partition
            scripts:  # list of commands to execute with {{.File}} referencing input file
            - ls -l {{.File}}
            - md5sum {{.File}}

Job execution is done on behalf of selected user with slurm --uid/--gid options, meaning generated script must be executed as root (sbatch xxx).

## License

Apache-2.0

## Credits

2021 Olivier Sallou <olivier.sallou@irisa.fr>
