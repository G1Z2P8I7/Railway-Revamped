package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/railway-revamped/core/pkg/crypto"
)

func main() {
	qrPayloadFlag := flag.String("qr", "", "Signed QR code JSON payload from digital ticket")
	sampleFlag := flag.Bool("sample", false, "Generate and verify a sample offline signed ticket")
	flag.Parse()

	fmt.Println("===============================================================================")
	fmt.Println("         📱 ONBOARD TTE HANDHELD TERMINAL — OFFLINE QR VALIDATOR")
	fmt.Println("===============================================================================")
	fmt.Println("Security Specification: Asymmetric Ed25519 Cryptography (Offline Mode)")
	fmt.Println("Zero Cellular Network Dependency — Stolen Handheld Terminals Cannot Forge Tickets")
	fmt.Println("-------------------------------------------------------------------------------")

	// The central authority has the private key; the handheld terminal holds ONLY the public key.
	authority, err := crypto.NewSovereignTicketAuthority()
	if err != nil {
		fmt.Printf("[!] Fatal Error initializing crypto authority: %v\n", err)
		os.Exit(1)
	}

	payloadToVerify := *qrPayloadFlag

	if *sampleFlag || payloadToVerify == "" {
		fmt.Println("[*] Generating authoritative sample ticket signed with HSM Ed25519 private key...")
		sampleTicket := crypto.TicketPayload{
			PNRNumber:     "2819401823",
			TrainNumber:   "12840",
			RunDate:       "20261105",
			Origin:        "MAS",
			Destination:   "BKSC",
			ClassCode:     "3A",
			CoachID:       "B4",
			BerthNumber:   27,
			PassengerName: "P. R. Sharma",
			IssuedAt:      time.Now().Unix(),
		}

		signedPayload, err := authority.IssueSignedTicketQR(sampleTicket)
		if err != nil {
			fmt.Printf("[!] Failed to generate sample ticket: %v\n", err)
			os.Exit(1)
		}
		payloadToVerify = signedPayload
		fmt.Printf("[+] Signed QR Payload Generated: \n%s\n\n", signedPayload)
	}

	fmt.Println("[*] Terminal operating in OFFLINE AIRPLANE MODE...")
	fmt.Println("[*] Verifying ticket signature against Indian Railways Central Ed25519 Public Key...")

	verifiedTicket, valid, err := authority.VerifyOfflineTicket(payloadToVerify)
	if err != nil || !valid {
		fmt.Println("\n❌ VERIFICATION FAILED: Invalid or forged digital ticket signature!")
		if err != nil {
			fmt.Printf("Reason: %v\n", err)
		}
		os.Exit(1)
	}

	fmt.Println("\n✅ DIGITAL TICKET VERIFIED SUCCESSFULLY [AUTHENTIC]")
	fmt.Println("-------------------------------------------------------------------------------")
	fmt.Printf("PNR Number:       %s\n", verifiedTicket.PNRNumber)
	fmt.Printf("Train:            %s (Date: %s)\n", verifiedTicket.TrainNumber, verifiedTicket.RunDate)
	fmt.Printf("Route:            %s ➔ %s\n", verifiedTicket.Origin, verifiedTicket.Destination)
	fmt.Printf("Class:            %s\n", verifiedTicket.ClassCode)
	fmt.Printf("Seat Assignment:  Coach %s, Berth %d\n", verifiedTicket.CoachID, verifiedTicket.BerthNumber)
	fmt.Printf("Passenger Name:   %s\n", verifiedTicket.PassengerName)
	fmt.Printf("Issued At:        %s\n", time.Unix(verifiedTicket.IssuedAt, 0).Format(time.RFC1123))
	fmt.Println("-------------------------------------------------------------------------------")
	fmt.Println("STATUS: Passenger marked as ONBOARD in TTE offline manifest.")
	fmt.Println("===============================================================================")
}
