# !/bin/bash
sudo go run . -trace /mnt/ssd/filtered-50K-no-cache-enable-snapshot-with-valuesize -outdir /mnt/ssd/raw_correlation/ -raw-window 500000

sudo go run . -trace /mnt/ssd/filtered-50K-no-cache-enable-snapshot-with-valuesize -outdir /mnt/ssd/raw_correlation/ -raw-window 1000000

sudo go run . -trace /mnt/ssd/filtered-50K-no-cache-enable-snapshot-with-valuesize -outdir /mnt/ssd/raw_correlation/ -raw-window 2000000

