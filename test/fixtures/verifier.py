#!/usr/bin/env python3
import argparse
import sys
import re


def evaluate(output: str) -> float:
    output = output.strip()

    if len(output) < 10:
        return 0.0

    score = 0.0

    score += min(len(output) / 500.0, 0.4)

    numbers = re.findall(r'\b\d+\.?\d*\b', output)
    score += min(len(numbers) * 0.05, 0.3)

    sentences = re.split(r'[.!?]+', output)
    well_formed = sum(1 for s in sentences if len(s.strip()) > 5)
    score += min(well_formed * 0.1, 0.3)

    return round(min(score, 1.0), 4)


def main():
    parser = argparse.ArgumentParser(description="TrustForge verifier example")
    parser.add_argument("--output", required=True, help="Path to model output file")
    parser.add_argument("--submission-id", required=True, help="Submission ID")
    args = parser.parse_args()

    try:
        with open(args.output, "r", encoding="utf-8") as f:
            model_output = f.read()
    except FileNotFoundError:
        print(f"ERROR: output file not found: {args.output}", file=sys.stderr)
        print("SCORE: 0.0")
        sys.exit(1)
    except Exception as e:
        print(f"ERROR: reading output: {e}", file=sys.stderr)
        print("SCORE: 0.0")
        sys.exit(1)

    score = evaluate(model_output)

    print(f"submission_id={args.submission_id}", file=sys.stderr)
    print(f"output_length={len(model_output)}", file=sys.stderr)
    print(f"computed_score={score}", file=sys.stderr)

    print(f"SCORE: {score}")


if __name__ == "__main__":
    main()
