export namespace app {
	
	export class AboutInfo {
	    appName: string;
	    appVersion: string;
	    appAuthor: string;
	    appURL: string;
	    bilibiliURL: string;
	    appDesc: string;
	    sponsorURL: string;
	    n2nVersion: string;
	    n2nBundledVer: string;
	    n2nNeedsUpgrade: boolean;
	    n2nFound: boolean;
	    tapVersion: string;
	    tapBundledVer: string;
	    tapNeedsUpgrade: boolean;
	    tapAdapterFound: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AboutInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.appName = source["appName"];
	        this.appVersion = source["appVersion"];
	        this.appAuthor = source["appAuthor"];
	        this.appURL = source["appURL"];
	        this.bilibiliURL = source["bilibiliURL"];
	        this.appDesc = source["appDesc"];
	        this.sponsorURL = source["sponsorURL"];
	        this.n2nVersion = source["n2nVersion"];
	        this.n2nBundledVer = source["n2nBundledVer"];
	        this.n2nNeedsUpgrade = source["n2nNeedsUpgrade"];
	        this.n2nFound = source["n2nFound"];
	        this.tapVersion = source["tapVersion"];
	        this.tapBundledVer = source["tapBundledVer"];
	        this.tapNeedsUpgrade = source["tapNeedsUpgrade"];
	        this.tapAdapterFound = source["tapAdapterFound"];
	    }
	}
	export class ConfigInfo {
	    community: string;
	    ip: string;
	    key_masked: string;
	    key_set: boolean;
	    supernode: string;
	
	    static createFrom(source: any = {}) {
	        return new ConfigInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.community = source["community"];
	        this.ip = source["ip"];
	        this.key_masked = source["key_masked"];
	        this.key_set = source["key_set"];
	        this.supernode = source["supernode"];
	    }
	}
	export class ConnectionDetails {
	    connected: boolean;
	    virtualIP: string;
	    nodeName: string;
	    status: string;
	    sponsorURL: string;
	
	    static createFrom(source: any = {}) {
	        return new ConnectionDetails(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connected = source["connected"];
	        this.virtualIP = source["virtualIP"];
	        this.nodeName = source["nodeName"];
	        this.status = source["status"];
	        this.sponsorURL = source["sponsorURL"];
	    }
	}
	export class NodeInfo {
	    name: string;
	    address: string;
	
	    static createFrom(source: any = {}) {
	        return new NodeInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.address = source["address"];
	    }
	}
	export class NodeLatencyInfo {
	    name: string;
	    address: string;
	    latency: number;
	
	    static createFrom(source: any = {}) {
	        return new NodeLatencyInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.address = source["address"];
	        this.latency = source["latency"];
	    }
	}
	export class TapUpgradeInfo {
	    needsUpgrade: boolean;
	    installedVersion: string;
	    bundledVersion: string;
	    dismissed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TapUpgradeInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.needsUpgrade = source["needsUpgrade"];
	        this.installedVersion = source["installedVersion"];
	        this.bundledVersion = source["bundledVersion"];
	        this.dismissed = source["dismissed"];
	    }
	}

}

