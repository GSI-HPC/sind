#!/bin/bash
#SBATCH --job-name=hello
#SBATCH --output=hello-%a.out
echo "task $SLURM_ARRAY_TASK_ID on $(hostname)"
sleep 20
