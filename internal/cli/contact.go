package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	contact := &cobra.Command{Use: "contact", Short: "Contatos e vínculo de identidades"}

	add := &cobra.Command{
		Use: "add <nome> <numero>", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, s, err := openStore()
			if err != nil {
				return err
			}
			defer closeStore(s)
			id, err := s.AddContact(args[0], args[1])
			if err != nil {
				return err
			}
			return emit(map[string]any{"id": id, "name": args[0]}, func() { printf("contato #%d %s\n", id, args[0]) })
		},
	}
	var q string
	list := &cobra.Command{
		Use: "list",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, s, err := openStore()
			if err != nil {
				return err
			}
			defer closeStore(s)
			cs, err := s.ListContacts(q)
			if err != nil {
				return err
			}
			return emit(cs, func() {
				for _, c := range cs {
					name := c.Name
					if name == "" {
						name = "(sem nome)"
					}
					printf("#%-4d %-30s %s\n", c.ID, name, strings.Join(c.JIDs, " "))
				}
			})
		},
	}
	list.Flags().StringVar(&q, "q", "", "filtro por nome ou jid")
	link := &cobra.Command{
		Use: "link <a> <b>", Short: "Junta duas identidades/contatos num só", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, s, err := openStore()
			if err != nil {
				return err
			}
			defer closeStore(s)
			id, err := s.LinkContacts(args[0], args[1])
			if err != nil {
				return err
			}
			return emit(map[string]any{"id": id}, func() { printf("vinculados no contato #%d\n", id) })
		},
	}
	rename := &cobra.Command{
		Use: "rename <ref> <nome>", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, s, err := openStore()
			if err != nil {
				return err
			}
			defer closeStore(s)
			if err := s.RenameContact(args[0], args[1]); err != nil {
				return err
			}
			return emit(map[string]any{"ok": true}, func() { printf("renomeado\n") })
		},
	}
	contact.AddCommand(add, list, link, rename)
	root.AddCommand(contact)
}
