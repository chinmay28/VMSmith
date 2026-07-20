package vm

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"text/template"
)

// Autounattend.xml generation for the unattended-install-from-ISO path
// (roadmap 5.6.11). Windows Setup scans the root of every attached
// removable drive — including the provisioning cdrom vmsmith always
// attaches — for a file named Autounattend.xml and, when present, runs the
// whole installation hands-free: disk partitioning (layout matched to the
// firmware), edition selection, locale, EULA, Administrator password, and
// first-logon commands that enable RDP and WinRM so the freshly installed
// guest is remotely manageable.

const autounattendTemplate = `<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend">
  <settings pass="windowsPE">
    <component name="Microsoft-Windows-International-Core-WinPE" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <SetupUILanguage>
        <UILanguage>{{.Locale}}</UILanguage>
      </SetupUILanguage>
      <InputLocale>{{.Locale}}</InputLocale>
      <SystemLocale>{{.Locale}}</SystemLocale>
      <UILanguage>{{.Locale}}</UILanguage>
      <UserLocale>{{.Locale}}</UserLocale>
    </component>
    <component name="Microsoft-Windows-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <DiskConfiguration>
        <Disk wcm:action="add" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
          <DiskID>0</DiskID>
          <WillWipeDisk>true</WillWipeDisk>
          <CreatePartitions>
{{- if .UEFI}}
            <CreatePartition wcm:action="add">
              <Order>1</Order>
              <Type>EFI</Type>
              <Size>200</Size>
            </CreatePartition>
            <CreatePartition wcm:action="add">
              <Order>2</Order>
              <Type>MSR</Type>
              <Size>16</Size>
            </CreatePartition>
            <CreatePartition wcm:action="add">
              <Order>3</Order>
              <Type>Primary</Type>
              <Extend>true</Extend>
            </CreatePartition>
{{- else}}
            <CreatePartition wcm:action="add">
              <Order>1</Order>
              <Type>Primary</Type>
              <Extend>true</Extend>
            </CreatePartition>
{{- end}}
          </CreatePartitions>
          <ModifyPartitions>
{{- if .UEFI}}
            <ModifyPartition wcm:action="add">
              <Order>1</Order>
              <PartitionID>1</PartitionID>
              <Format>FAT32</Format>
              <Label>System</Label>
            </ModifyPartition>
            <ModifyPartition wcm:action="add">
              <Order>2</Order>
              <PartitionID>3</PartitionID>
              <Format>NTFS</Format>
              <Label>Windows</Label>
            </ModifyPartition>
{{- else}}
            <ModifyPartition wcm:action="add">
              <Order>1</Order>
              <PartitionID>1</PartitionID>
              <Format>NTFS</Format>
              <Label>Windows</Label>
              <Active>true</Active>
            </ModifyPartition>
{{- end}}
          </ModifyPartitions>
        </Disk>
      </DiskConfiguration>
      <ImageInstall>
        <OSImage>
{{- if .ImageIndex}}
          <InstallFrom>
            <MetaData wcm:action="add" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
              <Key>/IMAGE/INDEX</Key>
              <Value>{{.ImageIndex}}</Value>
            </MetaData>
          </InstallFrom>
{{- end}}
          <InstallTo>
            <DiskID>0</DiskID>
            <PartitionID>{{if .UEFI}}3{{else}}1{{end}}</PartitionID>
          </InstallTo>
        </OSImage>
      </ImageInstall>
      <UserData>
        <AcceptEula>true</AcceptEula>
      </UserData>
    </component>
  </settings>
  <settings pass="specialize">
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <ComputerName>{{.ComputerName}}</ComputerName>
    </component>
    <component name="Microsoft-Windows-TerminalServices-LocalSessionManager" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <fDenyTSConnections>false</fDenyTSConnections>
    </component>
    <component name="Networking-MPSSVC-Svc" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <FirewallGroups>
        <FirewallGroup wcm:action="add" wcm:keyValue="RemoteDesktop" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
          <Active>true</Active>
          <Group>Remote Desktop</Group>
          <Profile>all</Profile>
        </FirewallGroup>
      </FirewallGroups>
    </component>
  </settings>
  <settings pass="oobeSystem">
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <OOBE>
        <HideEULAPage>true</HideEULAPage>
        <HideLocalAccountScreen>true</HideLocalAccountScreen>
        <HideOnlineAccountScreens>true</HideOnlineAccountScreens>
        <HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE>
        <ProtectYourPC>3</ProtectYourPC>
      </OOBE>
      <UserAccounts>
        <AdministratorPassword>
          <Value>{{.AdminPassword}}</Value>
          <PlainText>true</PlainText>
        </AdministratorPassword>
      </UserAccounts>
      <FirstLogonCommands>
        <SynchronousCommand wcm:action="add" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
          <Order>1</Order>
          <CommandLine>cmd /c winrm quickconfig -q -force</CommandLine>
          <Description>Enable WinRM</Description>
        </SynchronousCommand>
        <SynchronousCommand wcm:action="add" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
          <Order>2</Order>
          <CommandLine>cmd /c netsh advfirewall firewall set rule group="remote desktop" new enable=Yes</CommandLine>
          <Description>Open RDP firewall group</Description>
        </SynchronousCommand>
      </FirstLogonCommands>
    </component>
  </settings>
</unattend>
`

// autounattendParams feeds the Autounattend.xml template.
type autounattendParams struct {
	Locale        string
	UEFI          bool
	ImageIndex    int
	ComputerName  string
	AdminPassword string
}

// windowsComputerNameRe strips characters NetBIOS computer names reject.
var windowsComputerNameRe = regexp.MustCompile(`[^A-Za-z0-9-]`)

// windowsComputerName derives a valid Windows computer name (≤15 chars,
// alphanumeric + hyphen, not all-numeric) from a VM name.
func windowsComputerName(vmName string) string {
	name := windowsComputerNameRe.ReplaceAllString(vmName, "-")
	name = strings.Trim(name, "-")
	if len(name) > 15 {
		name = strings.Trim(name[:15], "-")
	}
	if name == "" || strings.Trim(name, "0123456789") == "" {
		name = "vmsmith-guest"
	}
	return name
}

// GenerateAutounattendXML renders the Autounattend.xml for an unattended
// Windows installation. uefi selects the GPT/EFI partition layout; the
// admin password lands XML-escaped so operator-chosen passwords cannot
// break the document.
func GenerateAutounattendXML(vmName, adminPassword, locale string, imageIndex int, uefi bool) (string, error) {
	if strings.TrimSpace(locale) == "" {
		locale = "en-US"
	}
	params := autounattendParams{
		Locale:        xmlEscapeAttr(locale),
		UEFI:          uefi,
		ImageIndex:    imageIndex,
		ComputerName:  windowsComputerName(vmName),
		AdminPassword: xmlEscapeAttr(adminPassword),
	}
	tmpl, err := template.New("autounattend").Parse(autounattendTemplate)
	if err != nil {
		return "", fmt.Errorf("parsing autounattend template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("rendering autounattend template: %w", err)
	}
	return buf.String(), nil
}
